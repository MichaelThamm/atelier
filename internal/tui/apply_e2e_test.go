package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// TestE2E_ApplyCmd_appliesACachedPlan runs the real handoff against real
// terraform on a local module, so the argv order is proven to work rather than
// only asserted.
func TestApplyCmd_appliesACachedPlanAgainstRealTerraform(t *testing.T) {
	if _, err := tfexec.Locate(); err != nil {
		t.Skip("no terraform:", err)
	}
	dir := t.TempDir()

	mod := filepath.Join(dir, "mod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "main.tf"), []byte(
		"variable \"greeting\" { default = \"hi\" }\noutput \"greeting\" { value = var.greeting }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(
		"module \"m\" {\n  source = \"./mod\"\n  greeting = \"hello\"\n}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	tf, err := tfexec.New(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	p := &TfexecPlanner{Tf: tf, WrapperDir: dir}
	ctx := t.Context()

	if err := p.EnsureInit(ctx); err != nil {
		t.Fatalf("EnsureInit: %v", err)
	}
	if _, err := p.Plan(ctx); err != nil {
		t.Fatalf("Plan: %v", err)
	}

	cmd := applyCmd(tf.ExecPath(), dir, planFilePath(dir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("handoff failed: %v\n%s", err, out)
	}
	got := string(out)
	if strings.Contains(got, "Too many command line arguments") {
		t.Fatalf("terraform rejected the argv:\n%s", got)
	}
	if !strings.Contains(got, "Apply complete!") {
		t.Errorf("apply did not complete:\n%s", got)
	}
}
