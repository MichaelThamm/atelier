package main

import (
	"testing"

	"github.com/MichaelThamm/atelier/internal/importer"
)

// --- resolveProviderSource ---

func TestResolveProviderSource_BareName(t *testing.T) {
	got := resolveProviderSource("juju")
	if got != "juju/juju" {
		t.Errorf("got %q, want juju/juju", got)
	}
}

func TestResolveProviderSource_FullSource(t *testing.T) {
	got := resolveProviderSource("hashicorp/aws")
	if got != "hashicorp/aws" {
		t.Errorf("got %q, want hashicorp/aws", got)
	}
}

func TestResolveProviderSource_Empty(t *testing.T) {
	got := resolveProviderSource("")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestResolveProviderSource_Whitespace(t *testing.T) {
	got := resolveProviderSource("  juju  ")
	if got != "juju/juju" {
		t.Errorf("got %q, want juju/juju", got)
	}
}

// --- validateVarKey ---

func TestValidateVarKey_Valid(t *testing.T) {
	for _, k := range []string{"model_uuid", "my_var", "a", "X123", "my-var"} {
		if err := validateVarKey(k); err != nil {
			t.Errorf("key %q: unexpected error: %v", k, err)
		}
	}
}

func TestValidateVarKey_Invalid(t *testing.T) {
	for _, k := range []string{"", "123abc", "my var", "foo@bar", "a.b", "a{b}"} {
		if err := validateVarKey(k); err == nil {
			t.Errorf("key %q: expected error, got nil", k)
		}
	}
}

func TestValidateVarKey_LeadingUnderscore(t *testing.T) {
	if err := validateVarKey("_private"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- typeList ---

func TestTypeList(t *testing.T) {
	rs := []importer.ListResource{
		{Type: "juju_application"},
		{Type: "juju_model"},
		{Type: "juju_integration"},
	}
	got := typeList(rs)
	want := []string{"juju_application", "juju_model", "juju_integration"}
	if len(got) != len(want) {
		t.Fatalf("got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("index %d: got %q, want %q", i, got[i], w)
		}
	}
}

func TestTypeList_Empty(t *testing.T) {
	got := typeList(nil)
	if len(got) != 0 {
		t.Errorf("got %d items, want 0", len(got))
	}
}

// --- configAttrNames ---

func TestConfigAttrNames(t *testing.T) {
	lr := importer.ListResource{
		ConfigAttrs: []importer.ConfigAttr{
			{Name: "model_uuid", Required: true},
			{Name: "name", Required: false},
		},
	}
	got := configAttrNames(lr)
	want := "model_uuid*, name"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestConfigAttrNames_Empty(t *testing.T) {
	got := configAttrNames(importer.ListResource{})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
