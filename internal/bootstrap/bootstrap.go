// Package bootstrap orchestrates the init + rehydrate flows.
//
// The package's job is sequencing — it depends on every other internal
// package — and exists as a single entry point so cmd/atelier and the TUI
// don't both reinvent the wheel.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/candidate"
	"github.com/MichaelThamm/atelier/internal/gitops"
	"github.com/MichaelThamm/atelier/internal/session"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// InitOptions captures the inputs to bootstrapping a wrapper (used by
// `atelier module add <url>`).
type InitOptions struct {
	WrapperDir  string // user's CWD
	Source      string // git URL or local path (LocalSource && Source = path)
	LocalSource bool
	Ref         string // user-supplied ref; empty → HEAD
	ModulePath  string // candidate path within the cloned repo; empty → pick interactively / auto-pick if one
	GitRunner   gitops.Runner
}

// Result is the output of either InitNew or LoadExisting.
type Result struct {
	State       *wrapper.State
	Candidates  []candidate.Candidate
	ResolvedSHA string
	LiteralRef  string
	Warnings    []string

	// RefBump is non-nil when LoadExisting detects the ref resolved to a
	// different SHA than session.json recorded. Empty when no session existed
	// or when the SHA hasn't changed.
	RefBump *RefBump

	// RefUnresolved is set by LoadExisting when the wrapper's pinned ref could
	// not be resolved on the remote (e.g. the branch/tag was deleted). The
	// TUI is still launched — in a degraded state whose variable schema is
	// unknown — so the user can switch to a valid ref. When non-nil, the TUI
	// auto-opens the ref-switch modal on startup.
	RefUnresolved *RefUnresolved
}

// RefUnresolved describes why a wrapper opened without a resolvable ref, and
// (when known) the refs that DO exist so the ref switcher can present them.
type RefUnresolved struct {
	// Ref is the pinned ref that could not be resolved.
	Ref string
	// Reason is a short, human-readable explanation for the status banner.
	Reason string
	// Available lists the human-friendly ref names the remote does have.
	// Empty when the remote itself was unreachable (network/repo error), in
	// which case there is nothing to offer.
	Available []string
	// Offline is true when the failure was reaching the remote at all (as
	// opposed to the remote answering but lacking the ref). Distinguishes a
	// deleted-ref banner from a connectivity banner.
	Offline bool
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
		src := opts.Source
		if !filepath.IsAbs(src) && opts.WrapperDir != "" {
			src = filepath.Join(opts.WrapperDir, src)
		}
		abs, err := filepath.Abs(src)
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

	// Warm-start fast path: if a previous clone is already checked out at the
	// resolved SHA, reuse it instead of deleting and re-cloning. ls-remote has
	// confirmed the remote hasn't moved, so the on-disk tree is current. This
	// turns the common case (re-opening a wrapper whose ref hasn't changed)
	// from a full network clone into a single ls-remote round-trip. A `.git`
	// check guards against spawning git on a missing/partial directory, and
	// (for sparse clones) we confirm the needed subdir is actually materialized
	// before trusting the cache.
	if resolvedSHA != "" {
		if _, statErr := os.Stat(filepath.Join(cloneDir, ".git")); statErr == nil {
			if head, herr := gitops.HeadSHA(ctx, opts.GitRunner, cloneDir); herr == nil && head == resolvedSHA {
				if opts.ModulePath == "" {
					return cloneDir, resolvedSHA, nil
				}
				if _, e := os.Stat(filepath.Join(cloneDir, opts.ModulePath)); e == nil {
					return cloneDir, resolvedSHA, nil
				}
				// SHA matches but the subdir isn't present (e.g. a prior
				// sparse clone narrowed to a different module); fall through
				// to re-clone with the correct sparse set.
			}
		}
	}

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
		Subdir: opts.ModulePath,
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
	req, err := ReadRequiredProviders(candidateDir)
	if err != nil {
		return nil, fmt.Errorf("read required_providers: %w", err)
	}
	wrappedSource := composeSource(sourceURL, modulePath, literalRef)

	providers := DefaultProviderBlocks(req)
	values := map[string]cty.Value{}
	state := &wrapper.State{
		Dir:               wrapperDir,
		ModuleBlockName:   ModuleBlockName(modulePath, repoBasename(sourceURL)),
		Source:            wrappedSource,
		Vars:              vars,
		Values:            values,
		Providers:         providers,
		RequiredProviders: req,
	}
	return state, nil
}

// PrepareStateFromMain builds a degraded wrapper.State without a module
// clone, for the case where the pinned ref no longer resolves on the remote
// (so we can't read the module's variable declarations). The resulting State
// preserves the wrapper's Source and existing user values (read from main.tf
// by the caller) but carries an EMPTY variable schema — the module can't be
// inspected until the user switches to a resolvable ref.
//
// Keeping this separate from PrepareState (which requires a real clone)
// documents that empty Vars here is intentional, not a read failure, and
// avoids masking genuine clone problems on the normal path.
func PrepareStateFromMain(wrapperDir, modulePath, literalRef, sourceURL string) *wrapper.State {
	return &wrapper.State{
		Dir:             wrapperDir,
		ModuleBlockName: ModuleBlockName(modulePath, repoBasename(sourceURL)),
		Source:          composeSource(sourceURL, modulePath, literalRef),
		Vars:            nil,
		Values:          map[string]cty.Value{},
	}
}

// ModulePrep is the result of PrepareModule: a fully-assembled module State
// plus the discovery context. State is nil (with Candidates populated) in the
// one non-error "caller must choose" case — several candidates exist and none
// was specified via opts.ModulePath.
type ModulePrep struct {
	State       *wrapper.State
	Candidates  []candidate.Candidate
	ModulePath  string // the resolved candidate sub-path (empty when State is nil)
	ResolvedSHA string
	Warnings    []string
}

// PrepareModule clones opts.Source, discovers module candidates, resolves the
// module sub-path, and assembles a wrapper.State. It is the shared front half
// of both `atelier module add` flows — the fresh bootstrap (InitNew) and the
// additive append (cmd/atelier). Centralising it here ensures a module added
// to an existing wrapper gets the same candidate discovery as the first one;
// skipping it was why same-subdir modules (e.g. repos whose Terraform lives
// under terraform/) were appended with an empty sub-path and thus no variables.
//
// Sub-path resolution mirrors Terraform module conventions:
//   - opts.ModulePath set → validated against the discovered candidates.
//   - unset and exactly one candidate → auto-picked.
//   - unset and several candidates → returns ModulePrep{State: nil,
//     Candidates: …} so the caller can prompt for --module. This is not an
//     error.
func PrepareModule(ctx context.Context, opts InitOptions) (*ModulePrep, error) {
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

	cands, warnings, err := candidate.Discover(cloneDir)
	if err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("no module candidates found in %s", opts.Source)
	}

	modulePath := opts.ModulePath
	if modulePath == "" {
		if len(cands) == 1 {
			modulePath = cands[0].Path
		} else {
			// Caller must pick. Return the candidate list; State remains nil.
			return &ModulePrep{
				Candidates:  cands,
				ResolvedSHA: sha,
				Warnings:    warnings,
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

	return &ModulePrep{
		State:       state,
		Candidates:  cands,
		ModulePath:  modulePath,
		ResolvedSHA: sha,
		Warnings:    warnings,
	}, nil
}

// InitNew runs the full init flow up to (but not including) launching the
// TUI: clone, discover candidates, build State, write wrapper files, save
// session.
func InitNew(ctx context.Context, opts InitOptions) (*Result, error) {
	prep, err := PrepareModule(ctx, opts)
	if err != nil {
		return nil, err
	}
	if prep.State == nil {
		// Multiple candidates — caller must pick with --module.
		return &Result{
			Candidates:  prep.Candidates,
			ResolvedSHA: prep.ResolvedSHA,
			LiteralRef:  opts.Ref,
			Warnings:    prep.Warnings,
		}, nil
	}

	state := prep.State
	modulePath := prep.ModulePath
	sha := prep.ResolvedSHA

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
		Variables:         ConvertVariables(state.Vars),
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
		State:       state,
		Candidates:  prep.Candidates,
		ResolvedSHA: sha,
		LiteralRef:  opts.Ref,
		Warnings:    prep.Warnings,
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
			return nil, fmt.Errorf("not a wrapper directory: run 'atelier module add <url>' to bootstrap")
		}
		srcURL, refStr := decomposeSource(pm.Source)
		prev = &session.Session{
			SourceURL:           srcURL,
			LiteralRef:          refStr,
			ModuleBlockName:     pm.ModuleBlockName,
			ModuleCandidatePath: modulePathFromSource(pm.Source),
		}
	}

	// Recover the module sub-path if it's missing. Sessions written by an
	// earlier auto-rehydrate (or any wrapper whose session.json predates this
	// fix) may have an empty ModuleCandidatePath, which makes PrepareState read
	// variables from the repository root instead of the module's subdirectory.
	// Re-derive it from the primary module block's source in main.tf.
	if prev.ModuleCandidatePath == "" {
		if pm, err := wrapper.ReadMain(wrapperDir, nil); err == nil && pm != nil {
			prev.ModuleCandidatePath = modulePathFromSource(pm.Source)
		}
	}

	if gitRunner == nil {
		gitRunner = &gitops.Git{}
	}

	// Re-clone the module so we can read variable declarations.
	cloneDir, currentSHA, err := ResolveAndClone(ctx, InitOptions{
		WrapperDir:  wrapperDir,
		Source:      prev.SourceURL,
		LocalSource: isLocalSource(prev.SourceURL),
		Ref:         prev.LiteralRef,
		ModulePath:  prev.ModuleCandidatePath,
		GitRunner:   gitRunner,
	})
	if err != nil {
		// The pinned ref could not be resolved. This is a recoverable
		// condition: open the TUI in a degraded state (unknown variable
		// schema, but user values from main.tf preserved) so the user can
		// switch to a valid ref instead of being stranded at the CLI.
		var refErr *gitops.RefNotFoundError
		if errors.As(err, &refErr) {
			return loadDegraded(wrapperDir, prev, &RefUnresolved{
				Ref:       prev.LiteralRef,
				Reason:    fmt.Sprintf("ref %q no longer exists on the remote", prev.LiteralRef),
				Available: refErr.Available,
			})
		}
		// The remote itself was unreachable (offline, DNS, auth, repo gone).
		// Also non-fatal — open degraded with a connectivity banner. We have
		// no ref list to offer in this case.
		return loadDegraded(wrapperDir, prev, &RefUnresolved{
			Ref:     prev.LiteralRef,
			Reason:  "couldn't reach the module remote: " + err.Error(),
			Offline: true,
		})
	}

	state, err := PrepareState(wrapperDir, cloneDir, prev.ModuleCandidatePath, currentSHA, prev.LiteralRef, prev.SourceURL)
	if err != nil {
		return nil, err
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

// loadDegraded builds a Result for a wrapper whose pinned ref could not be
// resolved. It assembles a schema-less State from main.tf alone (no clone),
// preserving the user's existing values and secrets so nothing is lost, and
// attaches the RefUnresolved marker so the TUI launches straight into the
// ref-switch modal. The wrapper's on-disk files are never mutated here — this
// is a read-only recovery path.
func loadDegraded(wrapperDir string, prev *session.Session, unresolved *RefUnresolved) (*Result, error) {
	state := PrepareStateFromMain(wrapperDir, prev.ModuleCandidatePath, prev.LiteralRef, prev.SourceURL)

	// Overlay user values and wired expressions from main.tf. Vars is nil, so
	// ReadMain can't type-check values against a schema; it recovers them
	// verbatim, which is exactly what we want to carry through the switch.
	if pm, err := wrapper.ReadMain(wrapperDir, state.Vars); err == nil && pm != nil {
		if pm.ModuleBlockName != "" {
			state.ModuleBlockName = pm.ModuleBlockName
		}
		for k, v := range pm.Values {
			state.Values[k] = v
		}
		state.UnknownAttrs = pm.UnknownAttrs
	}
	// Overlay secrets (best-effort).
	if secrets, err := wrapper.ReadSecrets(wrapperDir); err == nil {
		state.SecretValues = secrets
	}

	return &Result{
		State:         state,
		LiteralRef:    prev.LiteralRef,
		ResolvedSHA:   prev.ResolvedSHA, // last-known SHA from session, if any
		RefUnresolved: unresolved,
	}, nil
}

// ReadRequiredProviders parses the module's terraform { required_providers
// { ... } } block (if any) and returns it as a map.
func ReadRequiredProviders(dir string) (map[string]wrapper.RequiredProvider, error) {
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

// DefaultProviderBlocks turns a required_providers map into a list of empty
// ProviderBlock stubs, ready for the TUI to populate. Sensitive attributes
// aren't filled in here because the schema is fetched from terraform later;
// the TUI flow that has access to the schema populates Attributes.
func DefaultProviderBlocks(req map[string]wrapper.RequiredProvider) []wrapper.ProviderBlock {
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
	if s == "" || s == "." || s == ".." {
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

// modulePathFromSource extracts the "//<subdir>" module path from a
// Terraform git source string, returning "" when the source points at the
// repository root. It mirrors decomposeSource's path-stripping logic but
// returns the discarded subdirectory instead of the base URL.
func modulePathFromSource(source string) string {
	s := source
	if q := strings.Index(s, "?ref="); q >= 0 {
		s = s[:q]
	}
	s = strings.TrimPrefix(s, "git::")
	searchFrom := 0
	if schemeEnd := strings.Index(s, "://"); schemeEnd >= 0 {
		searchFrom = schemeEnd + 3
	}
	if idx := strings.Index(s[searchFrom:], "//"); idx >= 0 {
		return s[searchFrom+idx+2:]
	}
	return ""
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

// ModuleBlockName derives a valid HCL identifier from a directory path.
// When the path is "." (root module), fallbackName is used instead.
//
// The module sub-path basename is preferred, EXCEPT when it is just the
// conventional Terraform directory name ("terraform"/"tf"): most repos keep
// their module under terraform/, so using that basename makes every such
// module collide on the same block name (terraform, terraform_2, …), which is
// meaningless. In that case fallbackName (the repo basename) is used instead,
// so blocks read like their source — e.g. mimir_operators rather than
// terraform_2. A more specific sub-path (e.g. terraform/cos-lite) still keeps
// its own basename (cos_lite).
func ModuleBlockName(modulePath, fallbackName string) string {
	base := filepath.Base(modulePath)
	if base == "" || base == "." || isGenericModuleDir(base) {
		if fallbackName != "" && fallbackName != "." {
			base = fallbackName
		}
	}
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

// isGenericModuleDir reports whether name is a conventional, uninformative
// Terraform module directory name that shouldn't be used as a block label when
// a repo basename is available.
func isGenericModuleDir(name string) bool {
	switch strings.ToLower(name) {
	case "terraform", "tf":
		return true
	}
	return false
}

func candidatePaths(cands []candidate.Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Path
	}
	return out
}

// ConvertVariables adapts a []tfvars.Variable to the wrapper-package
// tfvarsLike interface.
func ConvertVariables(vars []tfvars.Variable) []wrapper.TFVar {
	out := make([]wrapper.TFVar, len(vars))
	for i, v := range vars {
		out[i] = v
	}
	return out
}

// isLocalSource reports whether a source string refers to a local filesystem
// path rather than a git remote.
func isLocalSource(src string) bool {
	return strings.HasPrefix(src, "./") ||
		strings.HasPrefix(src, "../") ||
		strings.HasPrefix(src, "/")
}
