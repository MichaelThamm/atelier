package tui

import (
	"testing"
)

// TestPlanNav_HomeEndJumpRows pins Home/End as the plan tree's top/bottom
// jump, replacing the g/G aliases dropped in 1825598.
func TestPlanNav_HomeEndJumpRows(t *testing.T) {
	m := plannedReady(t)
	// Expand the resource-type bucket so there are rows to jump across.
	m = feed(m, key("down"), key("enter"))
	rows := flattenedRows(m.activeTree())
	if len(rows) < 3 {
		t.Fatalf("setup: need at least 3 rows, got %d", len(rows))
	}

	m = feed(m, key("end"))
	if last := len(flattenedRows(m.activeTree())) - 1; m.planCursor != last {
		t.Errorf("after end, planCursor = %d; want %d", m.planCursor, last)
	}
	// End is idempotent, not a one-step move.
	m = feed(m, key("end"))
	if last := len(flattenedRows(m.activeTree())) - 1; m.planCursor != last {
		t.Errorf("after a second end, planCursor = %d; want %d", m.planCursor, last)
	}
	m = feed(m, key("home"))
	if m.planCursor != 0 {
		t.Errorf("after home, planCursor = %d; want 0", m.planCursor)
	}
}

// TestPlanNav_NoCtrlHalfPage pins that Ctrl+U/Ctrl+D no longer scroll. They
// are the bubbles textinput kill-line bindings inside an editor cell, and
// carrying a second meaning on the navigation panes made the contract
// ambiguous.
func TestPlanNav_NoCtrlHalfPage(t *testing.T) {
	for _, r := range []string{"ctrl+u", "ctrl+d"} {
		m := plannedReady(t)
		m = feed(m, key("down"), key("down"))
		before := m.planCursor
		m = feed(m, key(r))
		if m.planCursor != before {
			t.Errorf("%s moved the plan cursor %d → %d", r, before, m.planCursor)
		}
	}
}

// TestPlanCollapse_OnlyEnterAndSpace pins the collapse binding to enter and
// space. left/right used to toggle, so → on an expanded node collapsed it,
// contradicting the directional convention every tree UI follows.
func TestPlanCollapse_OnlyEnterAndSpace(t *testing.T) {
	for _, r := range []string{"left", "right", "h", "g", "G", "j", "k"} {
		m := plannedReady(t)
		rows := flattenedRows(m.activeTree())
		if len(rows[0].Node.Children) == 0 {
			t.Fatalf("setup: first row has no children to collapse")
		}
		before := rows[0].Node.Collapsed
		m = feed(m, key(r))
		if got := flattenedRows(m.activeTree())[0].Node.Collapsed; got != before {
			t.Errorf("%q toggled collapse (%v → %v); it must not", r, before, got)
		}
	}

	for _, r := range []string{"enter", " "} {
		m := plannedReady(t)
		before := flattenedRows(m.activeTree())[0].Node.Collapsed
		m = feed(m, key(r))
		if got := flattenedRows(m.activeTree())[0].Node.Collapsed; got == before {
			t.Errorf("%q did not toggle collapse (stayed %v)", r, got)
		}
	}
}
