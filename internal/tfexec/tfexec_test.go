package tfexec

import (
	"context"
	"os/exec"
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
