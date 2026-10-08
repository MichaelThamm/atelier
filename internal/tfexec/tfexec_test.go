package tfexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLocate(t *testing.T) {
	// If neither terraform nor tofu is on PATH, Locate should error with a
	// helpful message. If at least one is, it should return that path.
	terraform, terraformErr := exec.LookPath("terraform")
	tofu, tofuErr := exec.LookPath("tofu")

	got, err := Locate()
	if terraformErr == nil || tofuErr == nil {
		if err != nil {
			t.Fatalf("Locate returned error %v; expected one of %s, %s", err, terraform, tofu)
		}
		if got != terraform && got != tofu {
			t.Errorf("Locate = %q; expected terraform=%q or tofu=%q", got, terraform, tofu)
		}
		return
	}
	if err == nil {
		t.Errorf("Locate returned %q but neither terraform nor tofu is on PATH", got)
	}
}

func TestNewAndVersion_integration(t *testing.T) {
	// Skip if terraform isn't on PATH; otherwise verify CheckVersion works.
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH")
	}
	wd := t.TempDir()
	tf, err := New(wd, "")
	if err != nil {
		t.Fatal(err)
	}
	ver, err := tf.CheckVersion(context.Background())
	if err != nil {
		t.Fatalf("CheckVersion: %v (got version %q)", err, ver)
	}
	if ver == "" {
		t.Error("empty version string")
	}
}

func TestWriteTimestampHeader(t *testing.T) {
	// nil is a no-op (logging not configured).
	WriteTimestampHeader(nil)

	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("prior\n"); err != nil {
		t.Fatal(err)
	}
	WriteTimestampHeader(f)
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.HasPrefix(s, "prior\n") {
		t.Errorf("header overwrote prior content: %q", s)
	}
	if !strings.Contains(s, "=== action started at ") {
		t.Errorf("missing timestamp header: %q", s)
	}
}

func TestLogDirPath(t *testing.T) {
	wd := t.TempDir()
	if got, want := LogDirPath(wd), filepath.Join(wd, LogDir); got != want {
		t.Errorf("LogDirPath(%q) = %q; want %q", wd, got, want)
	}
	// A relative workdir still resolves to an absolute path so the TUI can show
	// a location the user can open from anywhere.
	if rel := LogDirPath("."); !filepath.IsAbs(rel) {
		t.Errorf("LogDirPath(%q) = %q; want an absolute path", ".", rel)
	}
}

// TestConfigureLogging_appendsAcrossSessions guards the durable-log contract:
// a later Atelier session must not overwrite the start of an earlier one's
// logs (init writes before the planner seeks to the end).
func TestConfigureLogging_appendsAcrossSessions(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH")
	}
	wd := t.TempDir()
	tf1, err := New(wd, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []*os.File{tf1.StderrFile(), tf1.StdoutFile()} {
		if _, err := f.WriteString("session-one\n"); err != nil {
			t.Fatal(err)
		}
	}
	tf2, err := New(wd, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []*os.File{tf2.StderrFile(), tf2.StdoutFile()} {
		if _, err := f.WriteString("session-two\n"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{StderrLogName, StdoutLogName} {
		got, err := os.ReadFile(filepath.Join(wd, LogDir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"session-one", "session-two"} {
			if !strings.Contains(string(got), want) {
				t.Errorf("%s missing %q; a session overwrote an earlier one: %q", name, want, got)
			}
		}
	}
}

// TestApply_autoApprove pins both apply modes of the `atelier apply`
// one-liner. Interactive (autoApprove=false) must let Terraform ask, so the
// argv has no -auto-approve/-input=false; autoApprove=true must pass both, for
// the non-interactive case. The stub binary records its argv, so this fails if
// someone routes the interactive path back through terraform-exec's
// always-auto-approving Apply.
func TestApplyDirect_autoApprove(t *testing.T) {
	for _, c := range []struct {
		name        string
		autoApprove bool
		want        []string
		unwant      []string
	}{
		{name: "interactive", autoApprove: false, unwant: []string{"-auto-approve", "-input=false"}},
		{name: "non-interactive", autoApprove: true, want: []string{"-auto-approve", "-input=false"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			argsFile := filepath.Join(dir, "argv")
			script := filepath.Join(dir, "fake-terraform")
			body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argsFile + "\"\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}

			tf, err := New(dir, script)
			if err != nil {
				t.Fatal(err)
			}
			if err := tf.ApplyDirect(context.Background(), c.autoApprove); err != nil {
				t.Fatalf("Apply: %v", err)
			}

			got, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatalf("read recorded argv: %v", err)
			}
			args := strings.Fields(string(got))
			if len(args) == 0 || args[0] != "apply" {
				t.Fatalf("argv = %q, want it to start with 'apply'", string(got))
			}
			for _, a := range c.want {
				if !slices.Contains(args, a) {
					t.Errorf("argv = %q; want %s", string(got), a)
				}
			}
			for _, a := range c.unwant {
				if slices.Contains(args, a) {
					t.Errorf("argv = %q; must not pass %s", string(got), a)
				}
			}
		})
	}
}

// TestApplyDirectCmd_gracefulInterrupt pins the Ctrl-C contract: a nil Cancel
// (rather than Go's default SIGKILL) and a bounded WaitDelay.
func TestApplyDirectCmd_gracefulInterrupt(t *testing.T) {
	cmd := applyDirectCmd(context.Background(), "terraform", t.TempDir(), false)
	if cmd.Cancel != nil {
		t.Error("Cancel must be nil; the terminal already signals the child, and a second SIGINT force-quits it")
	}
	if cmd.WaitDelay != applyInterruptGrace {
		t.Errorf("WaitDelay = %v; want %v so a hung apply is still killed", cmd.WaitDelay, applyInterruptGrace)
	}
}

func TestDebugEnabled(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"NO":    false,
		"Off":   false,
		"1":     true,
		"true":  true,
		"trace": true,
		" yes ": true,
	}
	for val, want := range cases {
		t.Setenv(DebugEnvVar, val)
		if got := debugEnabled(); got != want {
			t.Errorf("debugEnabled() with %s=%q = %v; want %v", DebugEnvVar, val, got, want)
		}
	}
}

// TestConfigureLogging_integration checks that New wires a stderr log file
// into the wrapper's .atelier/logs/ directory. Requires terraform on PATH
// because New locates a binary before configuring logging.
func TestConfigureLogging_integration(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH")
	}
	wd := t.TempDir()
	if _, err := New(wd, ""); err != nil {
		t.Fatal(err)
	}
	stderrLog := filepath.Join(wd, LogDir, StderrLogName)
	if _, err := os.Stat(stderrLog); err != nil {
		t.Errorf("expected stderr log at %s: %v", stderrLog, err)
	}
	stdoutLog := filepath.Join(wd, LogDir, StdoutLogName)
	if _, err := os.Stat(stdoutLog); err != nil {
		t.Errorf("expected stdout log at %s: %v", stdoutLog, err)
	}
	// Without ATELIER_DEBUG the trace log must not be created.
	traceLog := filepath.Join(wd, LogDir, TraceLogName)
	if _, err := os.Stat(traceLog); err == nil {
		t.Errorf("trace log %s created without %s set", traceLog, DebugEnvVar)
	}
}
