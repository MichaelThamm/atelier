package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// refsFixture is a stand-in remote ref set with overlapping prefixes and
// substrings, exercising the matcher's ordering rules.
var refsFixture = []string{
	"main",
	"maintenance",
	"develop",
	"feature/main-fix",
	"release-1.0",
	"cos-lite-model",
}

// openRefModalWithRefs opens the ref-switch modal on the primary module and
// lands the async ref list, leaving the filter in "browse" (show-all) state.
func openRefModalWithRefs(t *testing.T, refs []string) *Model {
	t.Helper()
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.RefSwitcher = &stubRefSwitcher{refs: refs}
	m = feed(m, key("R"))
	if !m.refModal {
		t.Fatal("expected ref modal to open on R")
	}
	// Simulate the async ListRefs fetch landing.
	m = feed(m, refsLoadedMsg{refs: refs})
	return m
}

// TestFilterRefs_substringPrefixFirst pins the matcher contract: substring
// match, prefix hits ordered before interior hits, original order preserved
// within each group (ADR-0025).
func TestFilterRefs_substringPrefixFirst(t *testing.T) {
	got := filterRefs(refsFixture, "main", "somecurrentref")
	want := []string{"main", "maintenance", "feature/main-fix"}
	if len(got) != len(want) {
		t.Fatalf("filterRefs(main) = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filterRefs(main) = %v; want %v (order matters)", got, want)
		}
	}

	// Case-insensitive.
	if len(filterRefs(refsFixture, "MAIN", "x")) != 3 {
		t.Error("filter should be case-insensitive")
	}

	// Interior-only match.
	if got := filterRefs(refsFixture, "model", "x"); len(got) != 1 || got[0] != "cos-lite-model" {
		t.Errorf("filterRefs(model) = %v; want [cos-lite-model]", got)
	}

	// No match.
	if got := filterRefs(refsFixture, "zzz", "x"); len(got) != 0 {
		t.Errorf("filterRefs(zzz) = %v; want empty", got)
	}
}

// TestFilterRefs_browseShowsAll verifies the "untouched" states — empty query
// and query-equals-current-ref — both return the whole list so the user can
// browse without typing.
func TestFilterRefs_browseShowsAll(t *testing.T) {
	if got := filterRefs(refsFixture, "", "main"); len(got) != len(refsFixture) {
		t.Errorf("empty query = %v; want all %d refs", got, len(refsFixture))
	}
	if got := filterRefs(refsFixture, "main", "main"); len(got) != len(refsFixture) {
		t.Errorf("query==curRef = %v; want all refs (untouched browse)", got)
	}
}

// TestRefModal_filtersAsYouType checks the field drives the match list live.
func TestRefModal_filtersAsYouType(t *testing.T) {
	m := openRefModalWithRefs(t, refsFixture)
	if len(m.refMatches) != len(refsFixture) {
		t.Fatalf("on open, refMatches = %v; want all refs (browse)", m.refMatches)
	}

	m = feed(m, key("m"), key("a"), key("i"), key("n"))
	if m.refInput.Value() != "main" {
		t.Fatalf("field = %q; want main", m.refInput.Value())
	}
	want := []string{"main", "maintenance", "feature/main-fix"}
	if len(m.refMatches) != len(want) || m.refMatches[0] != "main" {
		t.Fatalf("refMatches = %v; want %v", m.refMatches, want)
	}
	// Highlight resets to the top match when the set narrows.
	if m.refMatchCursor != 0 {
		t.Errorf("refMatchCursor = %d; want 0 after filtering", m.refMatchCursor)
	}
}

// TestRefModal_arrowsNavigateList checks Up/Down wrap around the match list
// without touching the input field.
func TestRefModal_arrowsNavigateList(t *testing.T) {
	m := openRefModalWithRefs(t, refsFixture)
	m = feed(m, key("down"))
	if m.refMatchCursor != 1 {
		t.Errorf("after down, cursor = %d; want 1", m.refMatchCursor)
	}
	m = feed(m, key("up"), key("up")) // 1 -> 0 -> wrap to last
	if m.refMatchCursor != len(m.refMatches)-1 {
		t.Errorf("after wrap, cursor = %d; want %d", m.refMatchCursor, len(m.refMatches)-1)
	}
	if m.refInput.Value() != "" {
		t.Errorf("navigation must not alter the field, got %q", m.refInput.Value())
	}
}

// TestRefModal_tabAutocompletes checks Tab fills the field with the highlighted
// match (caret at end) rather than switching.
func TestRefModal_tabAutocompletes(t *testing.T) {
	m := openRefModalWithRefs(t, refsFixture)
	m = feed(m, key("down"), key("down")) // highlight refsFixture[2] = develop
	want := m.refMatches[m.refMatchCursor]
	m = feed(m, key("tab"))
	if m.refInput.Value() != want {
		t.Errorf("after Tab, field = %q; want highlighted match %q", m.refInput.Value(), want)
	}
	if !m.refModal {
		t.Error("Tab must not close the modal or start a switch")
	}
	if m.refSwitching {
		t.Error("Tab must not start a ref switch")
	}
}

// TestRefModal_enterUsesTypedTextNotHighlight guards the free-text contract:
// Enter switches to exactly what's typed even when a different list row is
// highlighted (ADR-0025).
func TestRefModal_enterUsesTypedTextNotHighlight(t *testing.T) {
	m := openRefModalWithRefs(t, refsFixture)
	// Type a free-text ref that is not in the list (e.g. a SHA).
	for _, r := range "deadbeef" {
		m = feed(m, key(string(r)))
	}
	if m.refInput.Value() != "deadbeef" {
		t.Fatalf("field = %q; want deadbeef", m.refInput.Value())
	}
	m = feed(m, key("enter"))
	if m.refModal {
		t.Fatal("Enter should close the modal and start the switch")
	}
	if !m.refSwitching {
		t.Fatal("Enter with a new ref should start a switch")
	}
}

// TestRefModal_wordDeleteWired proves the field is the readline cell, not the
// old append/trim buffer: word-delete chords (Ctrl+W / Alt+Backspace) that the
// old buffer ignored now delete a word.
func TestRefModal_wordDeleteWired(t *testing.T) {
	m := openRefModalWithRefs(t, refsFixture)
	// A space-delimited value so the word boundary is unambiguous.
	m.refInput.SetValue("one two three")

	m = feed(m, key("ctrl+w"))
	if got := m.refInput.Value(); got != "one two " {
		t.Errorf("after Ctrl+W, field = %q; want %q", got, "one two ")
	}

	m = feed(m, key("alt+backspace"))
	if got := m.refInput.Value(); got != "one " {
		t.Errorf("after Alt+Backspace, field = %q; want %q", got, "one ")
	}
}
