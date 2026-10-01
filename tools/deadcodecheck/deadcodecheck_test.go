package deadcodecheck

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot returns the module root (the directory holding go.mod), so `go tool
// deadcode ./...` analyzes the whole module regardless of the test's working
// directory.
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

// TestNoUnreachableFunctions runs `go tool deadcode -test` over the module and
// fails if it reports any unreachable function. `-test` includes test binaries
// in the call graph, so functions used only by tests are correctly reachable —
// the check flags code reachable from nothing at all (neither the binary nor a
// test). It is a whole-program call-graph analysis, complementary to
// tools/codecheck's package-level reachability.
//
// deadcode is declared as a tool dependency in go.mod, so `go tool` resolves it
// from the module graph: a pinned version, no network fetch at run time, and no
// version string drifting in this file.
func TestNoUnreachableFunctions(t *testing.T) {
	root := repoRoot(t)

	cmd := exec.Command("go", "tool", "deadcode", "-test", "./...")
	cmd.Dir = root
	// Findings go to stdout; `go tool` progress goes to stderr. Reading stdout
	// alone keeps that chatter out of the report (and out of CombinedOutput,
	// which would misread a cold cache as a finding).
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go tool deadcode failed to run: %v\n%s", err, stderr.String())
	}

	// deadcode exits 0 even when it finds dead code, so the finding is the
	// non-empty report, not the exit status.
	if report := strings.TrimSpace(string(out)); report != "" {
		t.Errorf("deadcode found unreachable functions (reachable from neither the binary nor any test):\n%s\n\nDelete them, or add a test that exercises the function if it is public API.", report)
	}
}
