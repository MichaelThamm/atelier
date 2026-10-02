package wrapper

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestLintTFVars walks a bundle against a module schema and asserts which
// findings it reports. The nested cases are the point: `add --var-file`
// already catches an undeclared top-level name, but a renamed object field
// inside a preset would otherwise rot silently (ADR-0031).
func TestLintTFVars(t *testing.T) {
	vars := tfVarsVars(t, `
variable "name" { type = string }
variable "replicas" {
  type    = number
  default = 1
}
variable "worker" {
  type = object({
    replicas  = optional(number, 1)
    resources = optional(object({
      limits = optional(map(string), {})
    }), {})
  })
}
variable "workers" {
  type = map(object({
    replicas = optional(number, 1)
  }))
}
variable "fleet" {
  type = list(object({
    name = string
  }))
}
`)

	cases := []struct {
		name         string
		body         string
		wantUnknown  []string
		wantMismatch int
	}{
		{
			name: "valid nested values",
			body: `name = "demo"
worker = { replicas = 2, resources = { limits = { cpu = "1" } } }
workers = { a = { replicas = 1 } }
fleet = [{ name = "a" }]
`,
		},
		{
			// optional() fields may be omitted from a partial object: the
			// linter must not report a shape mismatch the way cty conversion
			// (which drops optional metadata) would.
			name: "partial object with only optional fields",
			body: `workers = { a = {} }
`,
		},
		{
			name:        "unknown top-level variable",
			body:        `bogus = 1`,
			wantUnknown: []string{"bogus"},
		},
		{
			name:        "unknown key in object",
			body:        `worker = { replicas = 2, bogus = 1 }`,
			wantUnknown: []string{"worker.bogus"},
		},
		{
			name:        "unknown key nested two levels deep",
			body:        `worker = { resources = { limits = { cpu = "1" }, bogus = 2 } }`,
			wantUnknown: []string{"worker.resources.bogus"},
		},
		{
			name:        "unknown key in map(object) element",
			body:        `workers = { a = { bogus = 1 } }`,
			wantUnknown: []string{"workers.a.bogus"},
		},
		{
			name:        "unknown key in list(object) element",
			body:        `fleet = [{ name = "a" }, { bogus = 1 }]`,
			wantUnknown: []string{"fleet[1].bogus"},
		},
		{
			name:         "scalar type mismatch",
			body:         `replicas = "three"`,
			wantMismatch: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "preset.tfvars")
			if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			diags, err := LintTFVars(path, vars)
			if err != nil {
				t.Fatalf("LintTFVars: %v", err)
			}
			if !slices.Equal(diags.Unknown, c.wantUnknown) {
				t.Errorf("Unknown = %v, want %v", diags.Unknown, c.wantUnknown)
			}
			if len(diags.Mismatched) != c.wantMismatch {
				t.Errorf("Mismatched = %v, want %d entries", diags.Mismatched, c.wantMismatch)
			}
		})
	}
}

// A missing file is an error for lint: the caller named a bundle that is not
// there, which the apply path tolerates (an absent optional bundle) but a lint
// run must not.
func TestLintTFVars_missingFile(t *testing.T) {
	vars := tfVarsVars(t, `variable "name" { type = string }`)
	if _, err := LintTFVars(filepath.Join(t.TempDir(), "absent.tfvars"), vars); err == nil {
		t.Error("expected an error for a missing bundle")
	}
}
