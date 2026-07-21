package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionsTF(t *testing.T) {
	// Unpinned: no version line (init picks latest from upstream).
	got := versionsTF("juju", "juju/juju", "")
	if strings.Contains(got, "version") {
		t.Errorf("unpinned scaffold should omit version:\n%s", got)
	}
	for _, want := range []string{"required_providers", `juju = {`, `source = "juju/juju"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	// Pinned: version line present.
	got = versionsTF("juju", "juju/juju", "~> 1.5")
	if !strings.Contains(got, `version = "~> 1.5"`) {
		t.Errorf("pinned scaffold should include version:\n%s", got)
	}
}

func TestProvidersTF(t *testing.T) {
	if got := providersTF("juju"); got != "provider \"juju\" {}\n" {
		t.Errorf("providersTF = %q", got)
	}
}

func TestScaffoldAndDetect(t *testing.T) {
	dir := t.TempDir()

	if HasProviderConfig(dir) {
		t.Fatal("empty dir should have no provider config")
	}
	if err := scaffoldProviderRoot(dir, "juju/juju", ""); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !HasProviderConfig(dir) {
		t.Error("scaffolded dir should be detected as having provider config")
	}
	for _, f := range []string{"versions.tf", "providers.tf"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}

func TestWriteIfAbsentDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "versions.tf")
	if err := os.WriteFile(p, []byte("USER CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Scaffolding must not overwrite an existing file.
	if err := scaffoldProviderRoot(dir, "juju/juju", ""); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "USER CONTENT" {
		t.Errorf("existing versions.tf was clobbered: %q", data)
	}
}

func TestLocalProviderName(t *testing.T) {
	cases := map[string]string{
		"juju/juju":                       "juju",
		"registry.terraform.io/juju/juju": "juju",
		"hashicorp/aws":                   "aws",
	}
	for in, want := range cases {
		if got := localProviderName(in); got != want {
			t.Errorf("localProviderName(%q) = %q, want %q", in, got, want)
		}
	}
}
