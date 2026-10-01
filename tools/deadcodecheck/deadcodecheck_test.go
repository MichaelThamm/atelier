package deadcodecheck

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// deadcodeVersion pins the analyzer so the gate is reproducible: deadcode is a
// main package, not an importable library, so it is run via `go run` at a fixed
// version. The pin matters twice over: v0.43.0 (the repo's indirect x/tools)
// panics on this module's syntax, and v0.50.0 requires Go 1.26, which would
// force a toolchain download. v0.49.0 is the newest that builds on the module's
// Go version.
const deadcodeVersion = "v0.49.0"

// repoRoot returns the module root (the directory holding go.mod), so deadcode
// analyzes the whole module regardless of the test's working directory.
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

// TestNoUnreachableFunctions runs `deadcode -test` over the module and fails if
// it reports any unreachable function. `-test` includes test binaries in the
// call graph, so functions used only by tests are correctly reachable — the
// check flags code reachable from nothing at all (neither the binary nor a
// test). It is a whole-program call-graph analysis, complementary to
// tools/codecheck's package-level reachability.
func TestNoUnreachableFunctions(t *testing.T) {
	root := repoRoot(t)

	cmd := exec.Command("go", "run", "golang.org/x/tools/cmd/deadcode@"+deadcodeVersion, "-test", "./...")
	cmd.Dir = root
	// Findings go to stdout; `go run`'s download and toolchain progress go to
	// stderr. Reading stdout alone keeps that chatter out of the report (and
	// out of CombinedOutput, which would misread a cold cache as a finding).
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("deadcode@%s failed to run: %v\n%s", deadcodeVersion, err, stderr.String())
	}

	// deadcode exits 0 even when it finds dead code, so the finding is the
	// non-empty report, not the exit status.
	if report := strings.TrimSpace(string(out)); report != "" {
		t.Errorf("deadcode found unreachable functions (reachable from neither the binary nor any test):\n%s\n\nDelete them, or add a test that exercises the function if it is public API.", report)
	}
}
