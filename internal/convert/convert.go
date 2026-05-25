// Package convert implements `atelier convert`: adopting an existing
// Terraform project into an Atelier-managed wrapper.
//
// Two paths:
//
//   - Adopt: the project already has a module {} block with a git source.
//     Atelier just creates .atelier/session.json and clones the upstream
//     so it can read variable declarations. No files are moved; existing
//     state is untouched.
//
//   - Relocate: the project is a flat root module with no git module call.
//     Files move into a subdirectory, a wrapper is generated pointing at it,
//     and state is migrated.
package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/session"
	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// ModuleSubdir is the subdirectory name where the existing .tf files are
// relocated during conversion.
const ModuleSubdir = "module"

// Options configures a convert operation.
type Options struct {
	// Dir is the root module directory to convert (typically CWD).
	Dir string
	// ModuleDir overrides the default subdirectory name ("module").
	ModuleDir string
}

// Result captures the outcome of a conversion.
type Result struct {
	State           *wrapper.State
	ModuleDir       string // empty for adopt path
	ResourcesMoved  int
	BackupStatePath string
	Adopted         bool // true if we took the adopt path (no relocation)
}

// Run executes the full convert flow.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("convert: Dir is required")
	}
	if opts.ModuleDir == "" {
		opts.ModuleDir = ModuleSubdir
	}

	// Step 1: Validate the directory looks like a Terraform root module.
	tfFiles, err := listTFFiles(opts.Dir)
	if err != nil {
		return nil, err
	}
	if len(tfFiles) == 0 {
		return nil, fmt.Errorf("no .tf files found in %s; nothing to convert", opts.Dir)
	}

	// Refuse if it already looks like an Atelier wrapper.
	if _, err := os.Stat(filepath.Join(opts.Dir, wrapper.AtelierDir)); err == nil {
		return nil, fmt.Errorf("directory already contains .atelier/; use 'atelier' to open")
	}

	// Step 2: Check if the project already has a module block with a git source.
	// If so, take the adopt path — no file relocation needed.
	if mod := findGitModuleBlock(opts.Dir); mod != nil {
		return runAdopt(ctx, opts, mod)
	}

	// Otherwise, take the relocate path.
	return runRelocate(ctx, opts)
}

// moduleBlockInfo holds the parsed info from an existing module {} block.
type moduleBlockInfo struct {
	BlockName  string // e.g. "cos"
	Source     string // e.g. "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite"
	SourceURL  string // just the repo URL
	Ref        string // extracted ref
	ModulePath string // subdir path within the repo
}

// runAdopt handles the case where the project already has a module {} block
// pointing at a git source. We just create .atelier/ metadata so `atelier`
// can open the project as a wrapper.
func runAdopt(ctx context.Context, opts Options, mod *moduleBlockInfo) (*Result, error) {
	if _, err := tfexec.Locate(); err != nil {
		return nil, err
	}

	// Clone the upstream module to read variable declarations.
	cloneDir, sha, err := bootstrap.ResolveAndClone(ctx, bootstrap.InitOptions{
		WrapperDir: opts.Dir,
		Source:     mod.SourceURL,
		Ref:        mod.Ref,
	})
	if err != nil {
		return nil, fmt.Errorf("clone upstream module: %w", err)
	}

	// Prepare state from the upstream module's variables.
	state, err := bootstrap.PrepareState(opts.Dir, cloneDir, mod.ModulePath, sha, mod.Ref, mod.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("prepare state: %w", err)
	}

	// Save session.json so `atelier` can open without re-discovering.
	if err := session.Save(opts.Dir, &session.Session{
		SourceURL:           mod.SourceURL,
		LiteralRef:          mod.Ref,
		ResolvedSHA:         sha,
		ModuleCandidatePath: mod.ModulePath,
		ModuleBlockName:     mod.BlockName,
		LastOpened:          time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &Result{
		State:   state,
		Adopted: true,
	}, nil
}

// runRelocate handles the case where the project is a flat root module with
// no git module call. Files are moved into a subdirectory and a wrapper is
// generated.
func runRelocate(ctx context.Context, opts Options) (*Result, error) {
	moduleDir := filepath.Join(opts.Dir, opts.ModuleDir)
	if _, err := os.Stat(moduleDir); err == nil {
		return nil, fmt.Errorf("target module directory %q already exists; pick a different name with --module-dir", opts.ModuleDir)
	}

	// Step 2: Back up state if present.
	var backupPath string
	statePath := filepath.Join(opts.Dir, "terraform.tfstate")
	hasState := false
	if _, err := os.Stat(statePath); err == nil {
		hasState = true
		backupPath = statePath + ".pre-convert-backup"
		data, err := os.ReadFile(statePath)
		if err != nil {
			return nil, fmt.Errorf("read state for backup: %w", err)
		}
		if err := os.WriteFile(backupPath, data, 0o644); err != nil {
			return nil, fmt.Errorf("write state backup: %w", err)
		}
	}

	// Step 3: Move .tf files and related artifacts into the module subdir.
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		return nil, fmt.Errorf("create module dir: %w", err)
	}
	movedFiles, err := relocateFiles(opts.Dir, moduleDir)
	if err != nil {
		return nil, fmt.Errorf("relocate files: %w", err)
	}
	if len(movedFiles) == 0 {
		return nil, fmt.Errorf("no files relocated; aborting")
	}

	// Step 4: Read variables, outputs, required_providers from the relocated module.
	vars, err := tfvars.LoadDir(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("parse variables from module: %w", err)
	}
	reqProviders, err := bootstrap.ReadRequiredProviders(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("read required_providers: %w", err)
	}
	outputNames, err := wrapper.DiscoverOutputNames(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("discover outputs: %w", err)
	}

	// Derive the module block name from the directory name.
	blockName := bootstrap.ModuleBlockName(opts.ModuleDir, filepath.Base(opts.Dir))
	source := "./" + opts.ModuleDir

	// Generate wrapper files.
	providers := bootstrap.DefaultProviderBlocks(reqProviders)
	if err := wrapper.Bootstrap(wrapper.BootstrapOptions{
		Dir:               opts.Dir,
		ModuleBlockName:   blockName,
		Source:            source,
		ModuleDir:         opts.ModuleDir,
		RequiredProviders: reqProviders,
		Providers:         providers,
		Variables:         bootstrap.ConvertVariables(vars),
		OutputNames:       outputNames,
	}); err != nil {
		return nil, fmt.Errorf("wrapper bootstrap: %w", err)
	}

	// Step 5: terraform init in the wrapper.
	tf, err := tfexec.New(opts.Dir, "")
	if err != nil {
		return nil, fmt.Errorf("locate terraform: %w", err)
	}
	if err := tf.Init(ctx); err != nil {
		return nil, fmt.Errorf("terraform init: %w", err)
	}

	// Step 6: State migration — reparent resources under module.<name>.
	var resourcesMoved int
	if hasState {
		resourcesMoved, err = migrateState(ctx, tf, blockName)
		if err != nil {
			return nil, fmt.Errorf("state migration: %w", err)
		}
	}

	// Step 7: Save session.json.
	state := &wrapper.State{
		Dir:               opts.Dir,
		ModuleBlockName:   blockName,
		Source:            source,
		Vars:              vars,
		Values:            map[string]cty.Value{},
		Providers:         providers,
		RequiredProviders: reqProviders,
		OutputNames:       outputNames,
	}
	if err := session.Save(opts.Dir, &session.Session{
		SourceURL:           source,
		ModuleCandidatePath: ".",
		ModuleBlockName:     blockName,
		LastOpened:          time.Now().UTC(),
	}); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}

	return &Result{
		State:           state,
		ModuleDir:       opts.ModuleDir,
		ResourcesMoved:  resourcesMoved,
		BackupStatePath: backupPath,
	}, nil
}

// migrateState reads the current state, extracts resource addresses from the
// root module, and moves them under the module namespace.
func migrateState(ctx context.Context, tf *tfexec.Terraform, moduleBlockName string) (int, error) {
	state, err := tf.Show(ctx)
	if err != nil {
		return 0, fmt.Errorf("terraform show: %w", err)
	}
	if state == nil || state.Values == nil || state.Values.RootModule == nil {
		return 0, nil // no state or empty state
	}

	// Collect resource addresses from the root module (not from child modules).
	var addresses []string
	for _, r := range state.Values.RootModule.Resources {
		addresses = append(addresses, r.Address)
	}

	moved := 0
	prefix := "module." + moduleBlockName + "."
	for _, addr := range addresses {
		// Skip resources already under a module (shouldn't happen for root
		// module resources, but be defensive).
		if strings.HasPrefix(addr, "module.") {
			continue
		}
		dst := prefix + addr
		if err := tf.StateMv(ctx, addr, dst); err != nil {
			return moved, fmt.Errorf("move %s → %s: %w", addr, dst, err)
		}
		moved++
	}
	return moved, nil
}

// findGitModuleBlock scans .tf files in dir for a module {} block whose
// source looks like a git URL. Returns the first one found, or nil if none.
func findGitModuleBlock(dir string) *moduleBlockInfo {
	entries, _ := os.ReadDir(dir)
	parser := hclparse.NewParser()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
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
			if block.Type != "module" || len(block.Labels) != 1 {
				continue
			}
			// Look for the source attribute.
			srcAttr, exists := block.Body.Attributes["source"]
			if !exists {
				continue
			}
			val, dd := srcAttr.Expr.Value(nil)
			if dd.HasErrors() || val.Type() != cty.String || val.IsNull() {
				continue
			}
			src := val.AsString()
			if !isGitSource(src) {
				continue
			}
			// Parse the source into components.
			info := &moduleBlockInfo{BlockName: block.Labels[0], Source: src}
			info.SourceURL, info.Ref = decomposeSource(src)
			info.ModulePath = extractModulePath(src)
			return info
		}
	}
	return nil
}

// isGitSource reports whether a Terraform module source looks like a git
// remote (as opposed to a local path or registry source).
func isGitSource(src string) bool {
	if strings.HasPrefix(src, "git::") {
		return true
	}
	if strings.HasPrefix(src, "github.com/") {
		return true
	}
	if strings.Contains(src, "://") && !strings.HasPrefix(src, "file://") {
		return true
	}
	return false
}

// decomposeSource splits a git module source into the base URL and ref.
// E.g. "git::https://github.com/org/repo.git//path?ref=v1" → ("https://github.com/org/repo.git", "v1")
func decomposeSource(s string) (url, ref string) {
	url = s
	if i := strings.Index(url, "?ref="); i >= 0 {
		ref = url[i+len("?ref="):]
		url = url[:i]
	}
	url = strings.TrimPrefix(url, "git::")
	// Strip the "//<path>" module path suffix. Skip past "://" scheme.
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

// extractModulePath extracts the subdir path from a git source.
// E.g. "git::https://host/repo.git//terraform/cos-lite?ref=v1" → "terraform/cos-lite"
func extractModulePath(src string) string {
	s := src
	// Strip ?ref= query.
	if i := strings.Index(s, "?ref="); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "git::")
	// Find "//" after the scheme.
	searchFrom := 0
	if i := strings.Index(s, "://"); i >= 0 {
		searchFrom = i + 3
	}
	if j := strings.Index(s[searchFrom:], "//"); j >= 0 {
		return s[searchFrom+j+2:]
	}
	return ""
}

// listTFFiles returns the names of .tf files at the top level of dir.
func listTFFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tf") {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// relocateFiles moves .tf files and related Terraform artifacts from src to
// dst. It moves: *.tf files, *.tfvars files (except secrets.auto.tfvars),
// terraform.tfstate*, .terraform.lock.hcl. It does NOT move: .terraform/,
// .atelier/, .git/, .gitignore.
func relocateFiles(src, dst string) ([]string, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, e := range entries {
		if e.IsDir() {
			// Move subdirectories that are part of the module (e.g., modules/).
			// Skip .terraform, .git, .atelier, and the destination itself.
			name := e.Name()
			if name == ".terraform" || name == ".git" || name == ".atelier" ||
				name == filepath.Base(dst) {
				continue
			}
			// Move module-internal subdirectories (templates, files, modules, etc.)
			oldPath := filepath.Join(src, name)
			newPath := filepath.Join(dst, name)
			if err := os.Rename(oldPath, newPath); err != nil {
				return moved, fmt.Errorf("move dir %s: %w", name, err)
			}
			moved = append(moved, name+"/")
			continue
		}
		name := e.Name()
		if shouldRelocateFile(name) {
			oldPath := filepath.Join(src, name)
			newPath := filepath.Join(dst, name)
			if err := os.Rename(oldPath, newPath); err != nil {
				return moved, fmt.Errorf("move %s: %w", name, err)
			}
			moved = append(moved, name)
		}
	}
	return moved, nil
}

// shouldRelocateFile decides whether a file should be moved into the module
// subdirectory.
func shouldRelocateFile(name string) bool {
	// Move .tf files.
	if strings.HasSuffix(name, ".tf") {
		return true
	}
	// Move .tfvars files (user variable definitions).
	if strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".tfvars.json") {
		return true
	}
	// Move the lock file.
	if name == ".terraform.lock.hcl" {
		return true
	}
	// Do NOT move terraform.tfstate — it stays for state migration.
	// Do NOT move .gitignore, README.md, etc.
	return false
}
