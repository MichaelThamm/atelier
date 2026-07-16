package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

func TestBootstrap_freshWrapper(t *testing.T) {
	dir := t.TempDir()

	required := mustVar(t, "model_uuid", "string", cty.NilVal, false)
	optional := mustVar(t, "internal_tls", "bool", cty.True, true)

	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "cos_lite",
		Source:          "git::https://example.com/m.git?ref=v1",
		RequiredProviders: map[string]RequiredProvider{
			"juju": {Source: "juju/juju", Version: ">= 0.10"},
		},
		Providers: []ProviderBlock{
			{
				Name:      "juju",
				LocalName: "juju",
				Attributes: []ProviderAttr{
					{Name: "controller_addresses", Sensitive: false},
					{Name: "password", Sensitive: true},
				},
			},
		},
		Variables: []TFVar{required, optional},
	}
	if err := Bootstrap(opts); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	// Check that all the files are present.
	for _, f := range []string{MainTF, ProvidersTF, VersionsTF, VariablesTF, GitignoreFile, ReadmeFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, AtelierDir)); err != nil {
		t.Errorf("expected .atelier/ to exist: %v", err)
	}

	main, _ := os.ReadFile(filepath.Join(dir, MainTF))
	if !strings.Contains(string(main), `module "cos_lite"`) {
		t.Errorf("main.tf missing module block; got:\n%s", main)
	}
	if !strings.Contains(string(main), "?ref=v1") {
		t.Errorf("main.tf missing source ref; got:\n%s", main)
	}

	providers, _ := os.ReadFile(filepath.Join(dir, ProvidersTF))
	pcontent := string(providers)
	if !strings.Contains(pcontent, `provider "juju"`) {
		t.Errorf("providers.tf missing juju block; got:\n%s", pcontent)
	}
	if !strings.Contains(pcontent, "var.juju_password") {
		t.Errorf("providers.tf missing var.juju_password traversal; got:\n%s", pcontent)
	}

	variables, _ := os.ReadFile(filepath.Join(dir, VariablesTF))
	vc := string(variables)
	if !strings.Contains(vc, `variable "juju_password"`) {
		t.Errorf("variables.tf missing juju_password; got:\n%s", vc)
	}
	if !strings.Contains(vc, "sensitive = true") {
		t.Errorf("variables.tf missing sensitive=true; got:\n%s", vc)
	}

	versions, _ := os.ReadFile(filepath.Join(dir, VersionsTF))
	vsc := string(versions)
	if !strings.Contains(vsc, `juju = {`) {
		t.Errorf("versions.tf missing juju entry; got:\n%s", vsc)
	}
	if !strings.Contains(vsc, `juju/juju`) {
		t.Errorf("versions.tf missing provider source; got:\n%s", vsc)
	}

	gi, _ := os.ReadFile(filepath.Join(dir, GitignoreFile))
	gic := string(gi)
	for _, want := range []string{".atelier/", "secrets.auto.tfvars", "*.tfstate"} {
		if !strings.Contains(gic, want) {
			t.Errorf(".gitignore missing %q; got:\n%s", want, gic)
		}
	}

	// This wrapper has a sensitive provider attribute, so the README must
	// carry the secrets-handling note.
	readme, _ := os.ReadFile(filepath.Join(dir, ReadmeFile))
	if !strings.Contains(string(readme), "secrets.auto.tfvars") {
		t.Errorf("README missing secrets note for a wrapper with secrets; got:\n%s", readme)
	}
}

func TestBootstrap_readmeOmitsSecretsNoteWhenNoSecrets(t *testing.T) {
	dir := t.TempDir()
	// No providers, no sensitive variables → the README should be clean.
	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "compute_only",
		Source:          "git::https://example.com/m.git?ref=v1",
		Variables: []TFVar{
			mustVar(t, "region", "string", cty.StringVal("us"), true),
		},
	}
	if err := Bootstrap(opts); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	readme, _ := os.ReadFile(filepath.Join(dir, ReadmeFile))
	if strings.Contains(string(readme), "secrets.auto.tfvars") {
		t.Errorf("README should not mention secrets when the wrapper has none; got:\n%s", readme)
	}
}

func TestBootstrap_readmeIncludesSecretsNoteForSensitiveVariable(t *testing.T) {
	dir := t.TempDir()
	sensitive := mustVar(t, "api_token", "string", cty.NilVal, false)
	sensitive.Sensitive = true
	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "svc",
		Source:          "git::https://example.com/m.git?ref=v1",
		Variables:       []TFVar{sensitive},
	}
	if err := Bootstrap(opts); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	readme, _ := os.ReadFile(filepath.Join(dir, ReadmeFile))
	if !strings.Contains(string(readme), "TF_VAR_<name>") {
		t.Errorf("README missing actionable secrets note for a sensitive variable; got:\n%s", readme)
	}
}

func TestBootstrap_doesNotOverwriteExistingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ReadmeFile), []byte("# Custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: "x",
		Source:          "git::https://example.com/m.git?ref=v1",
	}
	if err := Bootstrap(opts); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ReadmeFile))
	if string(got) != "# Custom" {
		t.Errorf("existing README was overwritten; got: %q", got)
	}
}

func TestBootstrap_validatesOptions(t *testing.T) {
	cases := []struct {
		name string
		opts BootstrapOptions
	}{
		{"no dir", BootstrapOptions{ModuleBlockName: "x", Source: "y"}},
		{"no module name", BootstrapOptions{Dir: "/tmp/x", Source: "y"}},
		{"no source", BootstrapOptions{Dir: "/tmp/x", ModuleBlockName: "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Bootstrap(c.opts); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestProviderVarName(t *testing.T) {
	cases := []struct{ provider, attr, want string }{
		{"juju", "password", "juju_password"},
		{"my-provider", "secret_key", "my_provider_secret_key"},
		{"aws", "session_token", "aws_session_token"},
	}
	for _, c := range cases {
		if got := providerVarName(c.provider, c.attr); got != c.want {
			t.Errorf("providerVarName(%q, %q) = %q, want %q", c.provider, c.attr, got, c.want)
		}
	}
}

func TestBootstrap_acceptsTfvarsVariable(t *testing.T) {
	// Compile-time check: tfvars.Variable must satisfy TFVar.
	var _ TFVar = tfvars.Variable{}
}
