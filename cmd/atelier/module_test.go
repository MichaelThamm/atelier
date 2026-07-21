package main

import (
	"testing"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// --- sanitizeBlockName ---

func TestSanitizeBlockName_Hyphens(t *testing.T) {
	got := sanitizeBlockName("my-module")
	if got != "my_module" {
		t.Errorf("got %q, want my_module", got)
	}
}

func TestSanitizeBlockName_Dots(t *testing.T) {
	got := sanitizeBlockName("my.module")
	if got != "my_module" {
		t.Errorf("got %q, want my_module", got)
	}
}

func TestSanitizeBlockName_LeadingDigits(t *testing.T) {
	got := sanitizeBlockName("123module")
	if got != "module" {
		t.Errorf("got %q, want module", got)
	}
}

func TestSanitizeBlockName_SpecialChars(t *testing.T) {
	got := sanitizeBlockName("foo@bar$baz{qux}")
	if got != "foobarbazqux" {
		t.Errorf("got %q, want foobarbazqux", got)
	}
}

func TestSanitizeBlockName_Spaces(t *testing.T) {
	got := sanitizeBlockName("my module")
	if got != "mymodule" {
		t.Errorf("got %q, want mymodule", got)
	}
}

func TestSanitizeBlockName_Empty(t *testing.T) {
	got := sanitizeBlockName("@#$")
	if got != "module" {
		t.Errorf("got %q, want module (fallback)", got)
	}
}

func TestSanitizeBlockName_LeadingDigitsAndSpecial(t *testing.T) {
	got := sanitizeBlockName("123@#$")
	if got != "module" {
		t.Errorf("got %q, want module (fallback)", got)
	}
}

func TestSanitizeBlockName_AlreadyValid(t *testing.T) {
	got := sanitizeBlockName("valid_name")
	if got != "valid_name" {
		t.Errorf("got %q, want valid_name", got)
	}
}

func TestSanitizeBlockName_Underscores(t *testing.T) {
	got := sanitizeBlockName("a_b_c")
	if got != "a_b_c" {
		t.Errorf("got %q, want a_b_c", got)
	}
}

// --- uniqueBlockName ---

func TestUniqueBlockName_NoCollision(t *testing.T) {
	got := uniqueBlockName("cos_lite", nil)
	if got != "cos_lite" {
		t.Errorf("got %q, want cos_lite", got)
	}
}

func TestUniqueBlockName_Collision(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "cos_lite"},
	}
	got := uniqueBlockName("cos_lite", existing)
	if got != "cos_lite_2" {
		t.Errorf("got %q, want cos_lite_2", got)
	}
}

func TestUniqueBlockName_MultipleCollisions(t *testing.T) {
	existing := []wrapper.ModuleBlockInfo{
		{Name: "cos_lite"},
		{Name: "cos_lite_2"},
		{Name: "cos_lite_3"},
	}
	got := uniqueBlockName("cos_lite", existing)
	if got != "cos_lite_4" {
		t.Errorf("got %q, want cos_lite_4", got)
	}
}

func TestUniqueBlockName_EmptyExisting(t *testing.T) {
	got := uniqueBlockName("anything", []wrapper.ModuleBlockInfo{})
	if got != "anything" {
		t.Errorf("got %q, want anything", got)
	}
}
