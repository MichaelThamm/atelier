package importer

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

// --- providerNames ---

func TestProviderNames_NilSchemas(t *testing.T) {
	got := providerNames(nil)
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestProviderNames_EmptySchemas(t *testing.T) {
	schemas := &tfjson.ProviderSchemas{Schemas: map[string]*tfjson.ProviderSchema{}}
	got := providerNames(schemas)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestProviderNames_Sorted(t *testing.T) {
	schemas := &tfjson.ProviderSchemas{Schemas: map[string]*tfjson.ProviderSchema{
		"registry.terraform.io/hashicorp/aws":  {},
		"registry.terraform.io/juju/juju":      {},
		"registry.terraform.io/hashicorp/null": {},
	}}
	got := providerNames(schemas)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0] != "registry.terraform.io/hashicorp/aws" {
		t.Errorf("got[0] = %q", got[0])
	}
	if got[1] != "registry.terraform.io/hashicorp/null" {
		t.Errorf("got[1] = %q", got[1])
	}
	if got[2] != "registry.terraform.io/juju/juju" {
		t.Errorf("got[2] = %q", got[2])
	}
}

func TestProviderNames_SingleProvider(t *testing.T) {
	schemas := &tfjson.ProviderSchemas{Schemas: map[string]*tfjson.ProviderSchema{
		"registry.terraform.io/juju/juju": {},
	}}
	got := providerNames(schemas)
	if len(got) != 1 || got[0] != "registry.terraform.io/juju/juju" {
		t.Errorf("got %v", got)
	}
}

// --- typeNames ---

func TestTypeNames_Empty(t *testing.T) {
	got := typeNames(nil)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestTypeNames_Single(t *testing.T) {
	rs := []ListResource{{Type: "juju_application"}}
	got := typeNames(rs)
	if len(got) != 1 || got[0] != "juju_application" {
		t.Errorf("got %v, want [juju_application]", got)
	}
}

func TestTypeNames_Multiple(t *testing.T) {
	rs := []ListResource{
		{Type: "juju_application"},
		{Type: "juju_model"},
		{Type: "aws_instance"},
	}
	got := typeNames(rs)
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
	if got[0] != "juju_application" || got[1] != "juju_model" || got[2] != "aws_instance" {
		t.Errorf("got %v", got)
	}
}

// --- SelectByType ---

func TestSelectByType_AllSelected(t *testing.T) {
	available := []ListResource{
		{Type: "juju_application"},
		{Type: "juju_model"},
	}
	selected, missing := SelectByType(available, nil)
	if len(selected) != 2 {
		t.Errorf("selected: got %d, want 2", len(selected))
	}
	if len(missing) != 0 {
		t.Errorf("missing: got %v, want empty", missing)
	}
}

func TestSelectByType_SpecificTypes(t *testing.T) {
	available := []ListResource{
		{Type: "juju_application"},
		{Type: "juju_model"},
		{Type: "aws_instance"},
	}
	selected, missing := SelectByType(available, []string{"juju_application", "juju_model"})
	if len(selected) != 2 {
		t.Errorf("selected: got %d, want 2", len(selected))
	}
	if len(missing) != 0 {
		t.Errorf("missing: got %v, want empty", missing)
	}
}

func TestSelectByType_SomeMissing(t *testing.T) {
	available := []ListResource{
		{Type: "juju_application"},
	}
	selected, missing := SelectByType(available, []string{"juju_application", "nonexistent"})
	if len(selected) != 1 {
		t.Errorf("selected: got %d, want 1", len(selected))
	}
	if len(missing) != 1 || missing[0] != "nonexistent" {
		t.Errorf("missing: got %v, want [nonexistent]", missing)
	}
}

func TestSelectByType_AllMissing(t *testing.T) {
	available := []ListResource{
		{Type: "juju_application"},
	}
	_, missing := SelectByType(available, []string{"aws_instance", "nonexistent"})
	if len(missing) != 2 {
		t.Errorf("missing: got %d, want 2", len(missing))
	}
}

func TestSelectByType_EmptyAvailable(t *testing.T) {
	selected, missing := SelectByType(nil, []string{"juju_application"})
	if len(selected) != 0 {
		t.Errorf("selected: got %d, want 0", len(selected))
	}
	if len(missing) != 1 || missing[0] != "juju_application" {
		t.Errorf("missing: got %v, want [juju_application]", missing)
	}
}
