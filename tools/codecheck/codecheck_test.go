// Package codecheck holds repository-wide structural checks that the compiler
// and go vet do not express: whether every internal package is reachable from
// the built binary.
//
// This exists because prose guidance did not hold. AGENTS.md already said to
// extend an existing mechanism rather than add a parallel one, yet an
// unreachable 700-line internal/convert package survived several refactors
// because nothing noticed it. A rule that lives only in a document is read
// once; a rule that fails `just check` is read every time.
//
// The check is deliberately narrow. It uses `go list` — the documented,
// version-accurate API for the module graph — rather than parsing source or
// shelling out to git, so it needs no extra dependency and cannot drift from
// the actual build. Broader architectural rules (forbidden imports, duplicate
// code) are left to review and the reuse table in AGENTS.md, or to
// golangci-lint if the project adopts it deliberately; the ecosystem-standard
// tools are better than a bespoke reimplementation, and lean-by-default says
// not to carry one.
package codecheck

import (
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const modulePrefix = "github.com/MichaelThamm/atelier"

// goListImports runs `go list -deps -json <patterns>` from the module root and
// returns the set of package import paths it listed. Failing to list is a test
// failure, not a skip: the check silently passing because `go list` could not
// run is exactly the failure mode it exists to prevent.
func goListImports(t *testing.T, root string, patterns ...string) map[string]bool {
	t.Helper()
	args := append([]string{"list", "-deps", "-json"}, patterns...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("go list %v failed: %v\n%s", patterns, err, ee.Stderr)
		}
		t.Fatalf("go list %v failed: %v", patterns, err)
	}

	pkgs := map[string]bool{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var pkg struct {
			ImportPath string
		}
		if err := dec.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode go list output: %v", err)
		}
		pkgs[pkg.ImportPath] = true
	}
	return pkgs
}

// TestNoDeadInternalPackages fails when an internal package is not reachable
// from the main binary's package graph. `go list -deps ./cmd/...` walks the
// exact imports the compiler follows, so anything internal it does not mention
// is dead product surface: it compiles and its tests may pass, but no command
// can reach it (this is how internal/convert survived).
func TestNoDeadInternalPackages(t *testing.T) {
	root := repoRoot(t)

	reachable := goListImports(t, root, "./cmd/...")
	if len(reachable) == 0 {
		t.Fatal("go list returned no packages under cmd/; is there a main package?")
	}
	all := goListImports(t, root, "./internal/...")
	if len(all) == 0 {
		t.Fatal("go list returned no packages under internal/; is the module layout correct?")
	}

	for pkg := range all {
		if !strings.HasPrefix(pkg, modulePrefix+"/internal/") {
			continue
		}
		if !reachable[pkg] {
			t.Errorf("internal package %s is not reachable from any main package in cmd/: it is dead product surface. Delete it or wire it into a command.", pkg)
		}
	}
}

// repoRoot returns the module root (the directory holding go.mod), so `go
// list`'s relative patterns resolve against the repository rather than the
// test package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("locate module root: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatal("not inside a Go module")
	}
	return filepath.Dir(gomod)
}
