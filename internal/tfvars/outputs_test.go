package tfvars

import "testing"

const sampleOutputs = `
output "greeting" {
  type        = string
  description = "A friendly greeting."
  value       = "hello"
}

output "endpoints" {
  value     = {}
  sensitive = true
}

output "legacy" {
  value      = var.name
  deprecated = "Use greeting instead."
}
`

func TestLoadModule_readsVariablesAndOutputs(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "variables.tf", sampleTF)
	writeTF(t, dir, "outputs.tf", sampleOutputs)

	mod, err := LoadModule(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mod.Variables) == 0 {
		t.Fatal("LoadModule dropped the variables")
	}
	if got := len(mod.Outputs); got != 3 {
		t.Fatalf("outputs = %d, want 3", got)
	}

	greeting := mod.Outputs[0]
	if greeting.Name != "greeting" {
		t.Errorf("first output = %q, want greeting", greeting.Name)
	}
	if greeting.Type.String() != "string" {
		t.Errorf("greeting type = %q, want string", greeting.Type.String())
	}
	if greeting.Description != "A friendly greeting." {
		t.Errorf("greeting description = %q", greeting.Description)
	}
	if greeting.ValueRaw != `"hello"` {
		t.Errorf("greeting ValueRaw = %q, want %q", greeting.ValueRaw, `"hello"`)
	}

	endpoints := mod.Outputs[1]
	if endpoints.Type.String() != "any" {
		t.Errorf("undeclared output type = %q, want any", endpoints.Type.String())
	}
	if !endpoints.Sensitive {
		t.Error("endpoints should be sensitive")
	}

	legacy := mod.Outputs[2]
	if legacy.Deprecated != "Use greeting instead." {
		t.Errorf("legacy deprecated = %q", legacy.Deprecated)
	}
	// The value references var.name, which Atelier does not evaluate; the load
	// must not fail.
	if legacy.ValueRaw != "var.name" {
		t.Errorf("legacy ValueRaw = %q, want var.name", legacy.ValueRaw)
	}
}

func TestLoadDir_stillReturnsOnlyVariables(t *testing.T) {
	dir := t.TempDir()
	writeTF(t, dir, "variables.tf", sampleTF)
	writeTF(t, dir, "outputs.tf", sampleOutputs)

	vars, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) == 0 {
		t.Fatal("LoadDir returned no variables")
	}
}
