package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestCellInput_RuneSafeBackspace verifies that Backspace deletes a whole
// rune (not a byte) when the buffer contains multi-byte characters. This
// is the regression test for the old append-only byte-buffer that would
// truncate "café" mid-codepoint.
func TestCellInput_RuneSafeBackspace(t *testing.T) {
	c := newCellInput("café", false, "")
	_, _ = c.Update(key("backspace"))
	if got := c.Value(); got != "caf" {
		t.Errorf("after backspace, value = %q; want %q", got, "caf")
	}
}

// TestCellInput_HomeEnd checks that Home parks the caret at byte 0 and
// End at the end of the buffer.
func TestCellInput_HomeEnd(t *testing.T) {
	c := newCellInput("hello", false, "")
	// Caret starts at end (length 5).
	if got := c.ti.Position(); got != 5 {
		t.Fatalf("initial caret = %d; want 5", got)
	}
	_, _ = c.Update(key("home"))
	if got := c.ti.Position(); got != 0 {
		t.Errorf("after home, caret = %d; want 0", got)
	}
	_, _ = c.Update(key("end"))
	if got := c.ti.Position(); got != 5 {
		t.Errorf("after end, caret = %d; want 5", got)
	}
}

// TestCellInput_WordJumpBack covers Ctrl+Left and Alt+B (both move one
// word back). On "hello world" with caret at end, one word-back goes to
// the start of "world" (position 6).
func TestCellInput_WordJumpBack(t *testing.T) {
	for _, k := range []string{"ctrl+left", "alt+b"} {
		c := newCellInput("hello world", false, "")
		_, _ = c.Update(key(k))
		if got := c.ti.Position(); got != 6 {
			t.Errorf("after %s, caret = %d; want 6", k, got)
		}
	}
}

// TestCellInput_WordJumpForward covers Ctrl+Right and Alt+F.
func TestCellInput_WordJumpForward(t *testing.T) {
	for _, k := range []string{"ctrl+right", "alt+f"} {
		c := newCellInput("hello world", false, "")
		// Send Home first so we have somewhere to walk forward to.
		_, _ = c.Update(key("home"))
		_, _ = c.Update(key(k))
		// The forward-word jump lands at the end of the first word
		// (position 5 in "hello world").
		if got := c.ti.Position(); got != 5 {
			t.Errorf("after %s, caret = %d; want 5", k, got)
		}
	}
}

// TestCellInput_WordDeleteBack covers Alt+Backspace and Ctrl+W: delete
// the previous word.
func TestCellInput_WordDeleteBack(t *testing.T) {
	for _, k := range []string{"alt+backspace", "ctrl+w"} {
		c := newCellInput("hello world", false, "")
		_, _ = c.Update(key(k))
		if got := c.Value(); got != "hello " {
			t.Errorf("after %s, value = %q; want %q", k, got, "hello ")
		}
	}
}

// TestCellInput_WordDeleteForward covers Alt+D: delete next word.
func TestCellInput_WordDeleteForward(t *testing.T) {
	c := newCellInput("hello world", false, "")
	_, _ = c.Update(key("home"))
	_, _ = c.Update(key("alt+d"))
	// After deleting "hello", the buffer is " world".
	if got := c.Value(); got != " world" {
		t.Errorf("after alt+d, value = %q; want %q", got, " world")
	}
}

// TestCellInput_KillToStart covers Ctrl+U: delete from caret to start.
func TestCellInput_KillToStart(t *testing.T) {
	c := newCellInput("hello world", false, "")
	_, _ = c.Update(key("ctrl+u"))
	if got := c.Value(); got != "" {
		t.Errorf("after ctrl+u from end, value = %q; want empty", got)
	}
}

// TestCellInput_KillToEnd covers Ctrl+K: delete from caret to end.
func TestCellInput_KillToEnd(t *testing.T) {
	c := newCellInput("hello world", false, "")
	_, _ = c.Update(key("home"))
	_, _ = c.Update(key("ctrl+k"))
	if got := c.Value(); got != "" {
		t.Errorf("after ctrl+k from start, value = %q; want empty", got)
	}
}

// TestCellInput_DeleteForward covers Delete: erase the character under
// the caret without moving the caret backward.
func TestCellInput_DeleteForward(t *testing.T) {
	c := newCellInput("hello", false, "")
	_, _ = c.Update(key("home"))
	_, _ = c.Update(key("delete"))
	if got := c.Value(); got != "ello" {
		t.Errorf("after delete from start, value = %q; want %q", got, "ello")
	}
}

// TestCellInput_AllowedRunes_FiltersDisallowed verifies that when
// allowedRunes is set (as numberEditor does), runes outside the set are
// dropped silently. textinput's own Validate hook only records an error
// and does not refuse the insertion, so the filtering happens at the
// cellInput boundary.
func TestCellInput_AllowedRunes_FiltersDisallowed(t *testing.T) {
	c := newCellInput("", false, "0123456789")
	_, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1', 'a', '2', 'b', '3'}})
	if got := c.Value(); got != "123" {
		t.Errorf("after typing '1a2b3' with numeric filter, value = %q; want %q", got, "123")
	}
}

// TestCellInput_Sensitive_EchoesBullets confirms that sensitive cells
// echo their content as bullets while Value() still returns the real
// buffer.
func TestCellInput_Sensitive_EchoesBullets(t *testing.T) {
	c := newCellInput("secret", true, "")
	if got := c.Value(); got != "secret" {
		t.Errorf("Value() should return real buffer, got %q", got)
	}
	view := c.View()
	if !strings.Contains(view, "••••••") {
		t.Errorf("sensitive view should render bullets; got %q", view)
	}
	if strings.Contains(view, "secret") {
		t.Errorf("sensitive view should not contain plaintext; got %q", view)
	}
}

// TestCellInput_View_WrapsInBrackets confirms the `[…]` rendering.
func TestCellInput_View_WrapsInBrackets(t *testing.T) {
	c := newCellInput("x", false, "")
	view := stripANSI(c.View())
	if len(view) < 2 || view[0] != '[' || view[len(view)-1] != ']' {
		t.Errorf("expected bracket wrap; got %q", view)
	}
}

// TestCellInput_Blurred_NoCaretPlaceholder guards that a blurred cell renders
// just its value — not textinput's end-of-line cursor placeholder, which
// otherwise made every unfocused cell read as `[3 ]` (trailing space).
func TestCellInput_Blurred_NoCaretPlaceholder(t *testing.T) {
	c := newCellInput("3", false, "")
	c.Blur()
	if got := stripANSI(c.View()); got != "[3]" {
		t.Errorf("blurred cell = %q; want \"[3]\" (no trailing caret space)", got)
	}

	s := newCellInput("ab", true, "")
	s.Blur()
	if got := stripANSI(s.View()); got != "[••]" {
		t.Errorf("blurred sensitive cell = %q; want \"[••]\"", got)
	}
}
