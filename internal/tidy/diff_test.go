package tidy

import (
	"strings"
	"testing"
)

func TestLineDiff_removal(t *testing.T) {
	old := "a\nb\nc\n"
	new := "a\nc\n"
	got := lineDiff(old, new)
	want := "  a\n- b\n  c\n"
	if got != want {
		t.Errorf("lineDiff =\n%q\nwant\n%q", got, want)
	}
}

func TestLineDiff_identical(t *testing.T) {
	s := "x\ny\n"
	got := lineDiff(s, s)
	if strings.Contains(got, "- ") || strings.Contains(got, "+ ") {
		t.Errorf("identical inputs should have no +/- lines, got:\n%s", got)
	}
}

func TestLineDiff_addition(t *testing.T) {
	got := lineDiff("a\n", "a\nb\n")
	if !strings.Contains(got, "+ b") {
		t.Errorf("expected an added line, got:\n%s", got)
	}
}
