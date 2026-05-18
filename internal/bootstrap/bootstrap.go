// Package bootstrap orchestrates the init + rehydrate flows.
//
// The package's job is sequencing — it depends on every other internal
// package — and exists as a single entry point so cmd/atelier and the TUI
// don't both reinvent the wheel.
package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/candidate"
	"github.com/canonical/atelier/internal/gitops"
	"github.com/canonical/atelier/internal/manifest"
	"github.com/canonical/atelier/internal/session"
	"github.com/canonical/atelier/internal/tfvars"
	"github.com/canonical/atelier/internal/wrapper"
)

// InitOptions captures the inputs to `atelier init <source>`.
type InitOptions struct {
	WrapperDir string // user's CWD
	Source     string // git URL or local path (LocalSource && Source = path)
	LocalSource bool
	Ref          string // user-supplied ref; empty → HEAD
	ModulePath   string // candidate path within the cloned repo; empty → pick interactively / auto-pick if one
	GitRunner    gitops.Runner
}

// Result is the output of either InitNew or LoadExisting.
type Result struct {
	State          *wrapper.State
	Candidates     []candidate.Candidate
	ResolvedSHA    string
	LiteralRef     string
	ManifestWarnings []string

	// RefBump is non-nil when LoadExisting detects the ref resolved to a
	// different SHA than session.json recorded. Empty when no session existed
	// or when the SHA hasn't changed.
	RefBump *RefBump
}

// RefBump records the SHA transition for SPEC §5.4 surfacing.
type RefBump struct {
	PreviousSHA string
	CurrentSHA  string
	LiteralRef  string
}

// CloneSubdir is the path under .atelier/ where Atelier shallow-clones
// module repositories.
func CloneSubdir(wrapperDir string) string {
	return filepath.Join(wrapperDir, ".atelier", "clone")
}

// ResolveAndClone clones the source into .atelier/clone/<basename> and
// returns the local clone directory plus the resolved SHA. If the source
// is a local path (opts.LocalSource), this is a no-op pointing at the local
// directory.
func ResolveAndClone(ctx context.Context, opts InitOptions) (cloneDir, resolvedSHA string, err error) {
	if opts.LocalSource {
		abs, err := filepath.Abs(opts.Source)
		if err != nil {
			return "", "", err
		}
		return abs, "", nil
	}
	if opts.GitRunner == nil {
		opts.GitRunner = &gitops.Git{}
	}

	// Resolve ref via ls-remote (so we capture the SHA for session.json).
	refs, err := gitops.LsRemote(ctx, opts.GitRunner, opts.Source)
	if err != nil {
		return "", "", err
	}
	ref := opts.Ref
	if ref == "" {
		// HEAD; ls-remote includes a "HEAD" entry.
		if sha, ok := refs["HEAD"]; ok {
			resolvedSHA = sha
		}
	} else {
		resolvedSHA, err = gitops.ResolveRef(ref, refs)
		if err != nil {
			return "", "", err
		}
	}

	repoName := repoBasename(opts.Source)
	cloneDir = filepath.Join(CloneSubdir(opts.WrapperDir), repoName)
	if err := os.MkdirAll(filepath.Dir(cloneDir), 0o755); err != nil {
		return "", "", err
	}
	// Remove any prior clone so we start fresh.
	if err := os.RemoveAll(cloneDir); err != nil {
		return "", "", err
	}
	cloneOpts := gitops.CloneOptions{
		URL:    opts.Source,
		Ref:    ref,
		Target: cloneDir,
	}
	if err := gitops.Clone(ctx, opts.GitRunner, cloneOpts); err != nil {
		return "", "", err
	}
	if resolvedSHA == "" {
		// Couldn't resolve from ls-remote (rare); pull from the clone HEAD.
		resolvedSHA, err = gitops.HeadSHA(ctx, opts.GitRunner, cloneDir)
		if err != nil {
			return cloneDir, "", err
		}
	}
	return cloneDir, resolvedSHA, nil
}

// PrepareState builds a wrapper.State from a clone directory, candidate
// path, and required-provider information. This is the pure assembly step:
// no git, no terraform. The caller has already cloned and (optionally) run
// `terraform init` to fetch providers.
func PrepareState(wrapperDir, cloneDir, modulePath, resolvedSHA, literalRef, sourceURL string) (*wrapper.State, error) {
	candidateDir := filepath.Join(cloneDir, modulePath)
	vars, err := tfvars.LoadDir(candidateDir)
	if err != nil {
		return nil, fmt.Errorf("read variables: %w", err)
	}
	req, err := readRequiredProviders(candidateDir)
	if err != nil {
		return nil, fmt.Errorf("read required_providers: %w", err)
	}
	outputNames, err := wrapper.DiscoverOutputNames(candidateDir)
	if err != nil {
		return nil, fmt.Errorf("read outputs: %w", err)
	}
	wrappedSource := composeSource(sourceURL, modulePath, literalRef)

	providers := defaultProviderBlocks(req)
	values := map[string]cty.Value{}
	state := &wrapper.State{
		Dir:               wrapperDir,
		ModuleBlockName:   moduleBlockName(modulePath),
		Source:            wrappedSource,
		Vars:              vars,
		Values:            values,
		Providers:         providers,
		RequiredProviders: req,
		OutputNames:       outputNames,
	}
	return state, nil
}

// InitNew runs the full init flow up to (but not including) launching the
// TUI: clone, discover candidates, build State, write wrapper files, save
// session.
func InitNew(ctx context.Context, opts InitOptions) (*Result, error) {
	if opts.WrapperDir == "" {
		return nil, fmt.Errorf("WrapperDir is required")
	}
	if opts.Source == "" {
		return nil, fmt.Errorf("Source is required")
	}

	cloneDir, sha, err := ResolveAndClone(ctx, opts)
	if err != nil {
		return nil, err
	}

	man, manWarnings, err := manifest.LoadFromRepo(cloneDir)
	if err != nil {
		return nil, err
	}
	cands, discoverWarnings, err := candidate.Discover(cloneDir, man)
	if err != nil {
		return nil, err
	}
	warnings := append(manWarnings, discoverWarnings...)
	if len(cands) == 0 {
		return nil, fmt.Errorf("no module candidates found in %s", opts.Source)
	}

	modulePath := opts.ModulePath
	if modulePath == "" {
		if len(cands) == 1 {
			modulePath = cands[0].Path
		} else {
			// Caller must pick. Return the candidate list; State remains nil.
			return &Result{
				Candidates:       cands,
				ResolvedSHA:      sha,
				LiteralRef:       opts.Ref,
				ManifestWarnings: warnings,
			}, nil
		}
	} else {
		// Validate the user-supplied module path against the candidate list.
		found := false
		for _, c := range cands {
			if c.Path == modulePath {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("--module %q is not among discovered candidates (%v)", modulePath, candidatePaths(cands))
		}
	}

	state, err := PrepareState(opts.WrapperDir, cloneDir, modulePath, sha, opts.Ref, opts.Source)
	if err != nil {
		return nil, err
	}

	// Convert variables to the wrapper-bootstrap adapter form.
	tfvarsLike := make([]any, len(state.Vars))
	for i := range state.Vars {
		tfvarsLike[i] = state.Vars[i]
	}
	if err := wrapper.Bootstrap(wrapper.BootstrapOptions{
		Dir:               opts.WrapperDir,
		ModuleBlockName:   state.ModuleBlockName,
		Source:            state.Source,
		ModuleDir:         modulePath,
		RequiredProviders: state.RequiredProviders,
		Providers:         state.Providers,
		Variables:         convertVariables(state.Vars),
		OutputNames:       state.OutputNames,
	}); err != nil {
		return nil, fmt.Errorf("wrapper bootstrap: %w", err)
	}

	// Save session.json.
	now := time.Now().UTC()
	if err := session.Save(opts.WrapperDir, &session.Session{
		SourceURL:           opts.Source,
		LiteralRef:          opts.Ref,
		ResolvedSHA:         sha,
		ModuleCandidatePath: modulePath,
		ModuleBlockName:     state.ModuleBlockName,
		LastOpened:          now,
	}); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &Result{
		State:            state,
		Candidates:       cands,
		ResolvedSHA:      sha,
		LiteralRef:       opts.Ref,
		ManifestWarnings: warnings,
	}, nil
}

// LoadExisting opens an existing wrapper directory. It re-reads session.json
// (auto-rehydrating if missing per SPEC §6.1), re-resolves the ref against
// the remote (best-effort; we don't error if offline), and parses main.tf to
// recover user-set values.
func LoadExisting(ctx context.Context, wrapperDir string, gitRunner gitops.Runner) (*Result, error) {
	prev, err := session.Load(wrapperDir)
	if err != nil {
		return nil, err
	}
	if prev == nil {
		// Auto-rehydrate. We need to discover the source from main.tf;
		// that requires reading without the variable list (we don't have
		// it yet). Read main.tf with an empty var list to get the source.
		pm, err := wrapper.ReadMain(wrapperDir, nil)
		if err != nil {
			return nil, err
		}
		if pm == nil {
			return nil, fmt.Errorf("not a wrapper directory: run 'atelier init <source>' to bootstrap")
		}
		srcURL, refStr := decomposeSource(pm.Source)
		prev = &session.Session{
			SourceURL:       srcURL,
			LiteralRef:      refStr,
			ModuleBlockName: pm.ModuleBlockName,
		}
	}

	if gitRunner == nil {
		gitRunner = &gitops.Git{}
	}

	// Re-clone the module so we can read variable declarations.
	cloneDir, currentSHA, err := ResolveAndClone(ctx, InitOptions{
		WrapperDir: wrapperDir,
		Source:     prev.SourceURL,
		Ref:        prev.LiteralRef,
		GitRunner:  gitRunner,
	})
	if err != nil {
		// Network failure during rehydrate is non-fatal; we can still open
		// the TUI but variables will be unknown. The caller should surface
		// this in the status pane.
		return nil, fmt.Errorf("rehydrate clone: %w", err)
	}

	state, err := PrepareState(wrapperDir, cloneDir, prev.ModuleCandidatePath, currentSHA, prev.LiteralRef, prev.SourceURL)
	if err != nil {
		return nil, err
	}

	// Ensure outputs.tf exists for wrappers bootstrapped before output
	// forwarding was implemented.
	if err := wrapper.EnsureOutputs(wrapperDir, state.ModuleBlockName, state.OutputNames); err != nil {
		return nil, fmt.Errorf("ensure outputs.tf: %w", err)
	}

	// Overlay user values from main.tf.
	pm, err := wrapper.ReadMain(wrapperDir, state.Vars)
	if err != nil {
		return nil, err
	}
	if pm != nil {
		state.ModuleBlockName = pm.ModuleBlockName
		for k, v := range pm.Values {
			state.Values[k] = v
		}
		state.UnknownAttrs = pm.UnknownAttrs
	}
	// Overlay secrets.
	if secrets, err := wrapper.ReadSecrets(wrapperDir); err == nil {
		state.SecretValues = secrets
	}

	res := &Result{
		State:       state,
		ResolvedSHA: currentSHA,
		LiteralRef:  prev.LiteralRef,
	}
	if prev.RefBumpedSince(currentSHA) {
		res.RefBump = &RefBump{
			PreviousSHA: prev.ResolvedSHA,
			CurrentSHA:  currentSHA,
			LiteralRef:  prev.LiteralRef,
		}
	}

	// Refresh session.json.
	prev.ResolvedSHA = currentSHA
	prev.LastOpened = time.Now().UTC()
	if err := session.Save(wrapperDir, prev); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}
	return res, nil
}

// readRequiredProviders parses the module's terraform { required_providers
// { ... } } block (if any) and returns it as a map.
func readRequiredProviders(dir string) (map[string]wrapper.RequiredProvider, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]wrapper.RequiredProvider{}
	parser := hclparse.NewParser()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		f, diags := parser.ParseHCL(data, e.Name())
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, block := range body.Blocks {
			if block.Type != "terraform" {
				continue
			}
			for _, sub := range block.Body.Blocks {
				if sub.Type != "required_providers" {
					continue
				}
				for name, attr := range sub.Body.Attributes {
					rp := parseRequiredProviderEntry(attr.Expr)
					if rp.Source != "" || rp.Version != "" {
						out[name] = rp
					}
				}
			}
		}
	}
	return out, nil
}

func parseRequiredProviderEntry(expr hcl.Expression) wrapper.RequiredProvider {
	// Required-providers entries are object literals: `juju = { source =
	// "juju/juju", version = ">= 0.10" }`. Some legacy modules use a string
	// shorthand; we don't support that here, but we tolerate parse failures
	// gracefully.
	val, diags := expr.Value(nil)
	if diags.HasErrors() || val.IsNull() {
		return wrapper.RequiredProvider{}
	}
	if !val.Type().IsObjectType() {
		return wrapper.RequiredProvider{}
	}
	m := val.AsValueMap()
	rp := wrapper.RequiredProvider{}
	if s, ok := m["source"]; ok && s.Type() == cty.String && !s.IsNull() {
		rp.Source = s.AsString()
	}
	if s, ok := m["version"]; ok && s.Type() == cty.String && !s.IsNull() {
		rp.Version = s.AsString()
	}
	return rp
}

// defaultProviderBlocks turns a required_providers map into a list of empty
// ProviderBlock stubs, ready for the TUI to populate. Sensitive attributes
// aren't filled in here because the schema is fetched from terraform later;
// the TUI flow that has access to the schema populates Attributes.
func defaultProviderBlocks(req map[string]wrapper.RequiredProvider) []wrapper.ProviderBlock {
	var out []wrapper.ProviderBlock
	for name := range req {
		out = append(out, wrapper.ProviderBlock{
			Name:      name,
			LocalName: name,
		})
	}
	return out
}

// repoBasename pulls a meaningful directory name from a git URL. For
// `git::https://x/y/z.git` it returns "z"; for `git@x:y/z.git` it returns
// "z"; for local paths it returns the directory basename.
func repoBasename(src string) string {
	s := src
	if strings.HasPrefix(s, "git::") {
		s = strings.TrimPrefix(s, "git::")
	}
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		s = "repo"
	}
	return s
}

// composeSource builds the canonical wrapper-source URL from a remote URL,
// candidate path, and ref.
func composeSource(remote, modulePath, ref string) string {
	url := remote
	if !strings.HasPrefix(url, "git::") &&
		!strings.HasPrefix(url, "./") &&
		!strings.HasPrefix(url, "../") &&
		!strings.HasPrefix(url, "/") {
		url = "git::" + url
	}
	if modulePath != "" && modulePath != "." {
		url += "//" + modulePath
	}
	if ref != "" {
		url += "?ref=" + ref
	}
	return url
}

// decomposeSource splits "git::https://...//path?ref=v1" into the base URL
// and ref. Used during rehydrate. The `//<modulepath>` separator is a
// Terraform convention indicating a subdirectory within the cloned repo.
func decomposeSource(s string) (url, ref string) {
	url = s
	if i := strings.Index(url, "?ref="); i >= 0 {
		ref = url[i+len("?ref="):]
		url = url[:i]
	}
	url = strings.TrimPrefix(url, "git::")
	// Strip the `//<path>` modulepath suffix. We need to skip the `://`
	// scheme separator first to avoid eating it.
	search := url
	offset := 0
	if i := strings.Index(search, "://"); i >= 0 {
		offset = i + 3
		search = url[offset:]
	}
	if j := strings.Index(search, "//"); j >= 0 {
		url = url[:offset+j]
	}
	return url, ref
}

// moduleBlockName derives a valid HCL identifier from a directory path.
func moduleBlockName(modulePath string) string {
	base := filepath.Base(modulePath)
	if base == "" || base == "." {
		base = "this"
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_':
			return r
		case r == '-':
			return '_'
		}
		return -1
	}, base)
	if out == "" {
		out = "this"
	}
	if c := out[0]; c >= '0' && c <= '9' {
		out = "m" + out
	}
	return out
}

func candidatePaths(cands []candidate.Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Path
	}
	return out
}

// convertVariables adapts a []tfvars.Variable to the wrapper-package
// tfvarsLike interface.
func convertVariables(vars []tfvars.Variable) []wrapper.TFVar {
	out := make([]wrapper.TFVar, len(vars))
	for i, v := range vars {
		out[i] = v
	}
	return out
}
