package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// --- map(string), line form ---
//
// lineMapEditor is the widget for a `map(string)` that sits inside another
// variable — a `config` or `storage_directives` field of an object. It edits
// the map as the HCL it is written as: one `key = value` per line.
//
//	mapEditor's two-column grid is the right widget for a map that owns the
//	right pane, where every row and the add affordance are visible at once. As
//	a field of a seven-field object it is the wrong shape. The keys hide behind
//	`(map: 2 entries)` until you drill in, the grid spends two columns on a key
//	and a value that are usually one word each, Enter means something different
//	in each column, and rows are sorted on load, so what the user wrote in
//	main.tf comes back reordered.
//
// Editing the line is closer to the file: what the widget shows is what the
// writer emits, and a value is edited where it sits.
//
//	↑/↓                  move between lines
//	Enter                append a line and move to it
//	Alt+Delete           delete the current line; a populated line asks for a
//	                     second Alt+Delete to confirm
//	(any readline edit)  routed to the focused line — see ADR-0020
type lineMapEditor struct {
	v      *tfvars.Variable
	lines  []cellInput
	cursor int

	// fresh marks lines appended in this session, as opposed to read from
	// main.tf. An untouched fresh line is silently abandoned when the user
	// moves away from it, so a stray Enter does not leave a blank row behind.
	fresh map[int]bool

	confirmDelete int
	nudge         string
}

func newLineMapEditor(v *tfvars.Variable, current cty.Value) *lineMapEditor {
	le := &lineMapEditor{v: v, fresh: map[int]bool{}, confirmDelete: -1}
	source := current
	if source == cty.NilVal || source.IsNull() {
		if v != nil && v.HasDefault && !v.Default.IsNull() {
			source = v.Default
		}
	}
	if source != cty.NilVal && !source.IsNull() && source.LengthInt() > 0 {
		// Iterate in sorted order because a cty map has no order of its own
		// and Go randomises map iteration, which would reshuffle the widget
		// on every repaint.
		m := source.AsValueMap()
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := ""
			if kv := m[k]; !kv.IsNull() && kv.Type() == cty.String {
				val = kv.AsString()
			}
			le.lines = append(le.lines, newCellInput(k+" = "+val, false, ""))
		}
	}
	if len(le.lines) == 0 {
		// Always one line to type into, so the empty map is editable without
		// a separate add-row cell to navigate to first.
		le.lines = append(le.lines, newCellInput("", false, ""))
		le.fresh[0] = true
	}
	return le
}

func (e *lineMapEditor) focusedLine() *cellInput {
	if e.cursor < 0 || e.cursor >= len(e.lines) {
		return nil
	}
	return &e.lines[e.cursor]
}

// onLastLine reports whether Enter would append, which is what the "+ Add line"
// hint advertises.
func (e *lineMapEditor) onLastLine() bool { return e.cursor == len(e.lines)-1 }

// abandonIfEmpty drops a fresh, untouched line the user moved away from. The
// last remaining line is kept, so the widget always has somewhere to type.
func (e *lineMapEditor) abandonIfEmpty() bool {
	if len(e.lines) <= 1 || !e.fresh[e.cursor] {
		return false
	}
	if strings.TrimSpace(e.lines[e.cursor].Value()) != "" {
		return false
	}
	e.removeLine(e.cursor)
	return true
}

func (e *lineMapEditor) removeLine(i int) {
	if i < 0 || i >= len(e.lines) {
		return
	}
	e.lines = append(e.lines[:i], e.lines[i+1:]...)
	// Indices shift, so mark every surviving populated line as not-fresh
	// rather than trying to remap the set.
	e.fresh = map[int]bool{}
	if e.cursor >= len(e.lines) {
		e.cursor = len(e.lines) - 1
	}
	if e.cursor < 0 {
		e.cursor = 0
	}
}

func (e *lineMapEditor) appendLine() {
	e.lines = append(e.lines, newCellInput("", false, ""))
	e.fresh[len(e.lines)-1] = true
	e.cursor = len(e.lines) - 1
}

func (e *lineMapEditor) deleteLine() {
	if strings.TrimSpace(e.lines[e.cursor].Value()) == "" {
		e.removeLine(e.cursor)
		e.nudge = ""
		return
	}
	if e.confirmDelete == e.cursor {
		e.removeLine(e.cursor)
		e.confirmDelete = -1
		e.nudge = ""
		return
	}
	e.confirmDelete = e.cursor
	e.nudge = "Alt+Delete again to remove"
}

func (e *lineMapEditor) Update(msg tea.Msg) (Editor, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}
	if !(k.Type == tea.KeyDelete && k.Alt) {
		e.confirmDelete = -1
	}
	switch {
	case k.Type == tea.KeyUp:
		e.nudge = ""
		e.abandonIfEmpty()
		if e.cursor > 0 {
			e.cursor--
		}
		return e, nil
	case k.Type == tea.KeyDown:
		e.nudge = ""
		if !e.abandonIfEmpty() && e.cursor < len(e.lines)-1 {
			e.cursor++
		}
		return e, nil
	case k.Type == tea.KeyEnter:
		e.nudge = ""
		e.appendLine()
		return e, nil
	case k.Type == tea.KeyDelete && k.Alt:
		e.deleteLine()
		return e, nil
	}
	if c := e.focusedLine(); c != nil {
		changed, cmd := c.Update(msg)
		if changed {
			e.nudge = ""
			delete(e.fresh, e.cursor)
		}
		return e, cmd
	}
	return e, nil
}

// AtTopLevel reports whether Esc/Tab should be owned by the model rather than
// the editor. lineMapEditor has no drill levels, so it is always at top level.
// Before yielding, it abandons a fresh empty line.
func (e *lineMapEditor) AtTopLevel() bool {
	e.abandonIfEmpty()
	return true
}

func (e *lineMapEditor) View() string {
	var b strings.Builder
	for i := range e.lines {
		focused := i == e.cursor
		text := strings.TrimSpace(e.lines[i].Value())
		var rendered string
		switch {
		case e.confirmDelete == i:
			rendered = styleMarkerRequired.Render(text + "   (delete?)")
		case focused:
			rendered = styleCursorActive.Render(e.lines[i].View())
		case text == "":
			rendered = styleHelp.Render("(empty line)")
		default:
			rendered = e.lines[i].View()
		}
		fmt.Fprintf(&b, "%s %s\n", rowStatusGlyph(text != "", e.fresh[i]), rendered)
	}
	if e.onLastLine() {
		fmt.Fprintf(&b, "  %s\n", styleHelp.Render("+ Enter to add a line"))
	}
	if e.nudge != "" {
		fmt.Fprintf(&b, "\n%s\n", styleMarkerRequired.Render(e.nudge))
	}
	// Kept short enough not to wrap: a wrapped hint adds a physical row to
	// the right pane. Tab is the app-wide pane switch and is in the ? modal.
	fmt.Fprint(&b, styleHelp.Render("[↑↓] line  [Enter] add  [Alt+Del] del  [?] keys"))
	return b.String()
}

// CursorLine reports the logical cursor row so the right pane can scroll to it.
func (e *lineMapEditor) CursorLine() int { return e.cursor }

// parseLine splits a `key = value` line on its first `=`. A line with no `=`
// is a key the user has not finished typing: it is not an entry, and not an
// error either — they are mid-edit.
func parseLine(line string) (key, value string, ok bool) {
	k, v, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	k = strings.TrimSpace(k)
	if k == "" {
		return "", "", false
	}
	return k, strings.TrimSpace(v), true
}

// CurrentValue folds the lines into a map. Unfinished lines are skipped: a
// half-typed line is the user's in-progress work, and writing a partial entry
// into main.tf would be worse than dropping it.
func (e *lineMapEditor) CurrentValue() cty.Value {
	m := map[string]cty.Value{}
	for i := range e.lines {
		if k, v, ok := parseLine(e.lines[i].Value()); ok {
			m[k] = cty.StringVal(v)
		}
	}
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	return cty.MapVal(m)
}

// lineMapKeys reports the parsed keys, for the parent row's preview.
func (e *lineMapEditor) lineMapKeys() []string {
	var keys []string
	for i := range e.lines {
		if k, _, ok := parseLine(e.lines[i].Value()); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}
