// Package codecheck holds repository-wide structural checks that are not
// covered by go vet or the compiler: dead packages, and duplicate
// implementations of mechanisms that should have exactly one home.
//
// These exist because prose guidance did not hold. AGENTS.md already said
// "extend an existing mechanism rather than adding a parallel one," yet four
// copies of module-source parsing and an unreachable 700-line package survived
// several refactors. A rule that only lives in a document is read once; a rule
// that fails `just check` is read every time.
package codecheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const modulePrefix = "github.com/MichaelThamm/atelier"

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod found upward)")
		}
		dir = parent
	}
}

// trackedGoFiles lists tracked (git ls-files) non-test .go files under dir,
// as paths relative to root. Tracking-based discovery keeps generated and
// gitignored trees out. It skips the test when git is unavailable.
func trackedGoFiles(t *testing.T, root, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--", dir)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" || !strings.HasSuffix(f, ".go") {
			continue
		}
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		files = append(files, f)
	}
	return files
}

// internalPackages returns the import path and directory of every tracked
// package under internal/ that has at least one non-test file.
func internalPackages(t *testing.T, root string) map[string]string {
	t.Helper()
	pkgs := map[string]string{}
	for _, f := range trackedGoFiles(t, root, "internal") {
		dir := filepath.Dir(f)
		pkgs[modulePrefix+"/"+filepath.ToSlash(dir)] = dir
	}
	return pkgs
}

// TestNoDeadInternalPackages fails when an internal package has no importer
// outside its own subtree and is not reachable from cmd/. Reachability starts
// at cmd/atelier and follows internal imports transitively.
//
// internal/convert survived for months this way: no command dispatched it, but
// nothing noticed because its tests kept it compiling and green.
func TestNoDeadInternalPackages(t *testing.T) {
	root := repoRoot(t)
	pkgs := internalPackages(t, root)

	// Forward edges: package -> internal packages it imports.
	deps := map[string][]string{}
	for pkg, dir := range pkgs {
		for _, f := range trackedGoFiles(t, root, dir) {
			data, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				t.Fatal(err)
			}
			deps[pkg] = append(deps[pkg], parseInternalImports(string(data))...)
		}
	}

	// Reachable set: everything reachable from cmd/ by following imports
	// transitively. cmd is the root, so a package the binary cannot reach is
	// dead product surface.
	reachable := map[string]bool{}
	var visit func(pkg string)
	visit = func(pkg string) {
		if reachable[pkg] {
			return
		}
		reachable[pkg] = true
		for _, next := range deps[pkg] {
			visit(next)
		}
	}
	for _, f := range trackedGoFiles(t, root, "cmd") {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parseInternalImports(string(data)) {
			visit(imp)
		}
	}

	for pkg, dir := range pkgs {
		if reachable[pkg] {
			continue
		}
		// A package is not dead merely because it is a leaf with no internal
		// importers — it may be imported by cmd. reachable already covers that,
		// so anything here is genuinely unreachable from the binary.
		t.Errorf("internal package %s has no import path from cmd/: it is dead product surface. Delete it or wire it into a command.", dir)
	}
}

// parseInternalImports returns the atelier internal import paths in a Go
// source file's import block, using a conservative regex rather than go/parser
// (import paths are one per line and unambiguous).
func parseInternalImports(src string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"(github\.com/MichaelThamm/atelier/internal/[^"]+)"`).FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

// TestModuleSourceParsingHasOneHome fails when the logic that splits a
// Terraform module source into remote/ref/subdir is re-implemented outside
// internal/modulesource. The tell is a second definition that splits on the
// `?ref=` query or the `//` separators.
func TestModuleSourceParsingHasOneHome(t *testing.T) {
	root := repoRoot(t)
	allowed := filepath.FromSlash("internal/modulesource/")

	for _, f := range trackedGoFiles(t, root, ".") {
		if strings.HasPrefix(f, allowed) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if strings.Contains(src, `"?ref="`) || strings.Contains(src, `"?ref=`) {
			t.Errorf("%s splits a module source on ?ref= instead of using internal/modulesource (Decompose/Compose/ModulePath)", f)
		}
	}
}

// TestAtomicWriteHasOneHome fails when a second temp-file-and-rename writer is
// introduced. wrapper.writeAtomic is the one implementation; sessions and
// state use their own only where the wrapper package cannot be imported, and
// those are allowlisted explicitly so a new copy is visible.
func TestAtomicWriteHasOneHome(t *testing.T) {
	root := repoRoot(t)
	// Files permitted to perform their own temp-file+rename, with a reason.
	allowed := map[string]bool{
		filepath.FromSlash("internal/wrapper/write.go"):   true, // the canonical implementation
		filepath.FromSlash("internal/session/session.go"): true, // leaf package: wrapper cannot be imported
		filepath.FromSlash("internal/state/state.go"):     true, // leaf package: wrapper cannot be imported
	}
	createTempRe := regexp.MustCompile(`os\.CreateTemp|ioutil\.TempFile`)
	renameRe := regexp.MustCompile(`os\.Rename`)

	for _, f := range trackedGoFiles(t, root, ".") {
		if allowed[f] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if createTempRe.MatchString(src) && renameRe.MatchString(src) {
			t.Errorf("%s implements its own temp-file-and-rename write; use wrapper.WriteMain or add this file to the allowlist with a reason", f)
		}
	}
}
