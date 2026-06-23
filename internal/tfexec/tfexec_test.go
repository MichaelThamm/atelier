package tfexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	stderrLog := filepath.Join(wd, LogDir, stderrLogName)
	if _, err := os.Stat(stderrLog); err != nil {
		t.Errorf("expected stderr log at %s: %v", stderrLog, err)
	}
	// Without ATELIER_DEBUG the trace log must not be created.
	traceLog := filepath.Join(wd, LogDir, traceLogName)
	if _, err := os.Stat(traceLog); err == nil {
		t.Errorf("trace log %s created without %s set", traceLog, DebugEnvVar)
	}
}
