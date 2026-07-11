// Package tidy implements `atelier tidy`: a headless prune of a wrapper's
// main.tf down to the sparse-plus-required form (ADR-0007, ADR-0021).
//
// It does not reimplement pruning. It rehydrates a wrapper.State from main.tf
// plus the upstream module schema (exactly as opening the TUI would), then
// renders main.tf through the same writer the TUI's save path uses. Preview
// and apply therefore go through one code path and can never diverge.
//
// tidy is deliberately conservative because it rewrites a file the user may
// have hand-authored: it is dry-run by default, refuses rather than guesses
// when the module schema or block is ambiguous, and backs up main.tf before
// writing.
package tidy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/gitops"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// Options configures a tidy run.
type Options struct {
	// Dir is the wrapper directory (the one containing main.tf).
	Dir string
	// Write applies the prune. When false (the default), tidy only computes
	// the diff and leaves main.tf untouched.
	Write bool
	// GitRunner overrides the git implementation used to fetch the module
	// schema. Nil uses the real git binary; tests inject a stub or rely on
	// local-path sources (which need no git).
	GitRunner gitops.Runner
}

// Result is the outcome of a tidy run.
type Result struct {
	// AlreadyTidy is true when main.tf is already in sparse form: nothing to
	// remove, no diff, no write.
	AlreadyTidy bool
	// Diff is a unified-style line diff of the change tidy would make (dry
	// run) or made (--write). Empty when AlreadyTidy.
	Diff string
	// BackupPath is the copy of the pre-tidy main.tf, set only when Write is
	// true and a change was applied.
	BackupPath string
	// Warnings are non-fatal advisories (e.g. an unpinned module ref).
	Warnings []string
}

// Run executes the tidy flow against opts.Dir.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Dir == "" {
		return nil, fmt.Errorf("tidy: Dir is required")
	}
	mainPath := filepath.Join(opts.Dir, wrapper.MainTF)
	current, err := os.ReadFile(mainPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("not a wrapper directory: no %s in %s", wrapper.MainTF, opts.Dir)
		}
		return nil, err
	}

	// Guardrail: tidy targets a single module block. A file with several is
	// ambiguous (which one is the wrapper's?) and outside v1's
	// one-module-per-wrapper model, so we refuse rather than silently tidy one.
	if n, err := countModuleBlocks(current, mainPath); err != nil {
		return nil, fmt.Errorf("parse %s: %w", wrapper.MainTF, err)
	} else if n == 0 {
		return nil, fmt.Errorf("no module block found in %s; nothing to tidy", wrapper.MainTF)
	} else if n > 1 {
		return nil, fmt.Errorf("found %d module blocks in %s; tidy supports a single-module wrapper only", n, wrapper.MainTF)
	}

	// Rehydrate State from main.tf + the upstream module schema. This is the
	// only way to know each variable's declared default; without the schema we
	// cannot tell a redundant value from a deliberate override, so a failure
	// here is fatal — tidy refuses rather than guesses.
	res, err := bootstrap.LoadExisting(ctx, opts.Dir, opts.GitRunner)
	if err != nil {
		return nil, fmt.Errorf("cannot determine module defaults (schema unavailable): %w", err)
	}
	state := res.State

	out := &Result{}

	// Advisory: defaults are only stable if the source is pinned. Against a
	// branch (or no ref) "default" is whatever upstream says today, so a value
	// tidy prunes now may diverge from upstream later.
	if w := refWarning(state.Source, res.LiteralRef); w != "" {
		out.Warnings = append(out.Warnings, w)
	}

	rendered, err := state.RenderMain()
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", wrapper.MainTF, err)
	}

	if bytes.Equal(normalize(current), normalize(rendered)) {
		out.AlreadyTidy = true
		return out, nil
	}
	out.Diff = lineDiff(string(current), string(rendered))

	if !opts.Write {
		return out, nil
	}

	// Back up before rewriting: tidy is information-lossy (a value explicitly
	// set to its default becomes indistinguishable from unset), so the user
	// must be able to recover the original.
	backup, err := backupMain(opts.Dir, current)
	if err != nil {
		return nil, fmt.Errorf("backup %s: %w", wrapper.MainTF, err)
	}
	out.BackupPath = backup

	if err := wrapper.WriteMain(opts.Dir, rendered); err != nil {
		return nil, fmt.Errorf("write %s: %w", wrapper.MainTF, err)
	}
	return out, nil
}

// countModuleBlocks returns how many `module "<name>"` blocks the HCL source
// declares.
func countModuleBlocks(src []byte, filename string) (int, error) {
	f, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return 0, fmt.Errorf("%s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return 0, nil
	}
	n := 0
	for _, b := range body.Blocks {
		if b.Type == "module" {
			n++
		}
	}
	return n, nil
}

// refWarning returns a non-fatal advisory when the module source is not pinned
// to an immutable commit, since the declared defaults tidy compares against
// can then move out from under the wrapper. Local-path sources have no remote
// to drift, so they are never warned about.
func refWarning(source, literalRef string) string {
	if isLocalSource(source) {
		return ""
	}
	if literalRef == "" {
		return "module source has no ref; defaults were resolved against the default-branch HEAD and may change as upstream moves. Pin a tag or commit for reproducible tidies."
	}
	if !isHexSHA(literalRef) {
		return fmt.Sprintf("module ref %q is not a commit SHA; defaults were resolved against its current state and may change as upstream moves. Pin a commit for reproducible tidies.", literalRef)
	}
	return ""
}

// backupMain copies the current main.tf into .atelier/backups/ with a
// timestamped name and returns the path written.
func backupMain(dir string, data []byte) (string, error) {
	backupDir := filepath.Join(dir, wrapper.AtelierDir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("main.tf.%s.bak", time.Now().UTC().Format("20060102-150405"))
	path := filepath.Join(backupDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// isLocalSource reports whether a module source refers to a local filesystem
// path rather than a git remote. Mirrors bootstrap's unexported check.
func isLocalSource(src string) bool {
	return strings.HasPrefix(src, "./") ||
		strings.HasPrefix(src, "../") ||
		strings.HasPrefix(src, "/")
}

// isHexSHA reports whether s is a full 40-character lowercase hex commit SHA.
func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// normalize trims trailing whitespace per line and a trailing final newline so
// the already-tidy check ignores cosmetic formatting differences between the
// on-disk file and a freshly rendered one.
func normalize(b []byte) []byte {
	lines := strings.Split(string(b), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	return []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n"))
}
