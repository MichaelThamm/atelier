package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePresetsLintArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantMod string
		want    []string
		wantErr bool
	}{
		{name: "module flag", args: []string{"--module", "terraform", "a.tfvars"}, wantMod: "terraform", want: []string{"a.tfvars"}},
		{name: "module equals", args: []string{"--module=terraform", "a.tfvars", "b.tfvars"}, wantMod: "terraform", want: []string{"a.tfvars", "b.tfvars"}},
		{name: "missing module", args: []string{"a.tfvars"}, wantErr: true},
		{name: "missing files", args: []string{"--module", "terraform"}, wantErr: true},
		{name: "unknown flag", args: []string{"--module", "terraform", "--bogus", "a.tfvars"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, err := parsePresetsLintArgs(c.args)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %v", c.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse(%v): %v", c.args, err)
			}
			if opts.Module != c.wantMod {
				t.Errorf("Module = %q, want %q", opts.Module, c.wantMod)
			}
			if len(opts.Files) != len(c.want) {
				t.Fatalf("Files = %v, want %v", opts.Files, c.want)
			}
			for i := range c.want {
				if opts.Files[i] != c.want[i] {
					t.Errorf("Files[%d] = %q, want %q", i, opts.Files[i], c.want[i])
				}
			}
		})
	}
}

// TestRunPresetsLint exercises the command end to end against a real module
// directory: a clean bundle passes, a bundle naming a removed nested field
// fails. This is the gate the bundled module gallery relies on.
func TestRunPresetsLint(t *testing.T) {
	dir := t.TempDir()
	moduleDir := filepath.Join(dir, "module")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vars := `
variable "name" { type = string }
variable "worker" {
  type = object({
    replicas = optional(number, 1)
  })
}
`
	if err := os.WriteFile(filepath.Join(moduleDir, "variables.tf"), []byte(vars), 0o644); err != nil {
		t.Fatal(err)
	}

	good := filepath.Join(dir, "good.tfvars")
	if err := os.WriteFile(good, []byte("name = \"demo\"\nworker = { replicas = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPresetsLint([]string{"--module", moduleDir, good}); err != nil {
		t.Errorf("clean preset must pass: %v", err)
	}

	bad := filepath.Join(dir, "bad.tfvars")
	if err := os.WriteFile(bad, []byte("name = \"demo\"\nworker = { replicas_ = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runPresetsLint([]string{"--module", moduleDir, bad}); err == nil {
		t.Error("a preset naming a removed nested field must fail")
	}
}
