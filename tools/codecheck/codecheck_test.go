// Package codecheck holds repository-wide structural checks that go vet does
// not express: whether every internal package is reachable from the built
// binary.
//
// Uses `go list`, the documented API for the module graph, so the check cannot
// drift from the actual build. Broader rules — forbidden imports, duplicate
// code — are left to review and the reuse table in AGENTS.md; the
// ecosystem-standard tools beat a bespoke reimplementation.
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

// goListImports returns the set of package import paths `go list -deps -json`
// reports for the given patterns. A failure is fatal, not a skip: the check
// silently passing because `go list` could not run would defeat its purpose.
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
// from the main binary. `go list -deps ./cmd/...` follows the exact imports the
// compiler does, so anything internal it omits cannot be reached by any
// command.
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
