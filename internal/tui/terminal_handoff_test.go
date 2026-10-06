package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// TestApplyCmd_usesApprovalFlagsOnTheCachedPlan pins the argv `A` hands to
// terraform: the cached plan file plus `-auto-approve -input=false`. The plan
// was already read in the tree, so pressing A is the confirmation and
// terraform must not gate it a second time (ADR-0052).
func TestApplyCmd_usesApprovalFlagsOnTheCachedPlan(t *testing.T) {
	dir := t.TempDir()
	planFile := planFilePath(dir)
	cmd := applyCmd("/usr/bin/terraform", dir, planFile)

	// Flags must precede the plan file: terraform takes one positional
	// argument and reads a flag after it as a second.
	want := []string{"apply", "-auto-approve", "-input=false", planFile}
	// Args[0] is the binary itself; the rest is what terraform is told.
	got := cmd.Args[1:]
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("terraform args = %v; want %v", got, want)
	}
	if n := len(got); n > 0 {
		if flagAfterPositional := got[n-1]; strings.HasPrefix(flagAfterPositional, "-") {
			t.Errorf("last arg %q is a flag; the plan file must be last", flagAfterPositional)
		}
	}
	if cmd.Dir != dir {
		t.Errorf("working directory = %q; want the wrapper %q", cmd.Dir, dir)
	}
}

// TestApplyCmd_planFile_livesInTheWrapper guards the handoff from pointing
// terraform at a cache path outside the directory it runs in.
func TestApplyCmd_planFile_livesInTheWrapper(t *testing.T) {
	dir := t.TempDir()
	planFile := planFilePath(dir)
	if !strings.HasPrefix(planFile, dir+string(filepath.Separator)) {
		t.Errorf("planFilePath(%q) = %q; want a path inside the wrapper", dir, planFile)
	}
}

// TestApplyCmd_missingPlanFileReportsError covers the case where the plan
// cache is cleared between P and A: the user gets the error in the footer
// rather than a silently skipped apply.
func TestApplyCmd_missingPlanFileReportsError(t *testing.T) {
	dir := t.TempDir() // no .atelier/cache/plan.tfplan written
	p := &TfexecPlanner{Tf: &tfexec.Terraform{}, WrapperDir: dir}

	cmd := p.ApplyCmd()
	if cmd == nil {
		t.Fatal("ApplyCmd returned nil; want an error command")
	}
	msg, ok := cmd().(applyErrorMsg)
	if !ok {
		t.Fatalf("got %T; want applyErrorMsg", cmd())
	}
	if !strings.Contains(msg.err.Error(), "no saved plan file") {
		t.Errorf("err = %v; want it to name the missing plan file", msg.err)
	}
}

// TestApplyCmd_unconfiguredReportsError keeps a nil Applier from panicking.
func TestApplyCmd_unconfiguredReportsError(t *testing.T) {
	var p *TfexecPlanner
	cmd := p.ApplyCmd()
	if cmd == nil {
		t.Fatal("ApplyCmd on a nil planner returned nil")
	}
	if _, ok := cmd().(applyErrorMsg); !ok {
		t.Errorf("got %T; want applyErrorMsg", cmd())
	}
}

// TestApplyCmd_runsInTheWrapperDir is the end-to-end check that the handoff
// really executes: a stub "terraform" records the argv it was given.
func TestApplyCmd_runsInTheWrapperDir(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available to stub terraform with")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	stub := filepath.Join(dir, "terraform")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\nprintf 'PWD=%s\\n' \"$PWD\" >> " + argvFile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := applyCmd(stub, dir, planFilePath(dir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("handoff failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{"-auto-approve\n", "-input=false\n", "PWD=" + dir + "\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("stub terraform did not receive %q; argv file:\n%s", want, got)
		}
	}
}
