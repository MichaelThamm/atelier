package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/wrapper"
	"github.com/zclconf/go-cty/cty"
)

// RefUnresolvedInfo mirrors bootstrap.RefUnresolved for the TUI layer: it
// records why the wrapper opened without a resolvable ref so the model can
// auto-open the switch modal, seed the hint, and render an explanatory banner.
type RefUnresolvedInfo struct {
	Ref       string
	Reason    string
	Available []string
	Offline   bool
}

// handleRefModalKey routes keys while the ref input prompt is visible. The
// input field is the canonical readline cell (ADR-0020); navigation keys drive
// the filtered ref list and Tab autocompletes, while everything else is
// forwarded to the cell so caret motion, word-jumps, and word-delete behave
// exactly as they do in a value editor (ADR-0025).
func (m *Model) handleRefModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.refModal = false
		return m, nil
	case tea.KeyEnter:
		// Enter always commits exactly what is typed — never the highlighted
		// list row — so free-text (a SHA, an unlisted ref) is always reachable
		// (ADR-0025). Picking from the list is the separate Tab gesture.
		newRef := strings.TrimSpace(m.refInput.Value())
		_, _, curRef, _ := m.activeRefInfo()
		if newRef == "" || newRef == curRef {
			m.refModal = false
			return m, nil
		}
		m.refModal = false
		m.refSwitching = true
		m.refErr = ""
		m.status = ""
		return m, tea.Batch(m.startRefSwitch(newRef), spinnerTick())
	case tea.KeyUp, tea.KeyCtrlP:
		if n := len(m.refMatches); n > 0 {
			m.refMatchCursor = (m.refMatchCursor - 1 + n) % n
		}
		return m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		if n := len(m.refMatches); n > 0 {
			m.refMatchCursor = (m.refMatchCursor + 1) % n
		}
		return m, nil
	case tea.KeyPgUp:
		// Jump a full window up, clamping at the top (no wrap).
		if n := len(m.refMatches); n > 0 {
			m.refMatchCursor -= refMatchWindow
			if m.refMatchCursor < 0 {
				m.refMatchCursor = 0
			}
		}
		return m, nil
	case tea.KeyPgDown:
		// Jump a full window down, clamping at the bottom (no wrap).
		if n := len(m.refMatches); n > 0 {
			m.refMatchCursor += refMatchWindow
			if m.refMatchCursor > n-1 {
				m.refMatchCursor = n - 1
			}
		}
		return m, nil
	case tea.KeyTab:
		// Autocomplete: fill the field with the highlighted match and park the
		// caret at the end, leaving the user free to edit or confirm.
		if m.refMatchCursor >= 0 && m.refMatchCursor < len(m.refMatches) {
			m.refInput.SetValue(m.refMatches[m.refMatchCursor])
			m.refreshRefMatches()
		}
		return m, nil
	}
	// Any other key is a cell edit. Re-filter only when the text actually
	// changed, so caret-only moves (←/→, Home/End) don't reset the highlight.
	prev := m.refInput.Value()
	m.refInput.Update(msg)
	if m.refInput.Value() != prev {
		m.refreshRefMatches()
	}
	return m, nil
}

// refreshRefMatches recomputes the filtered, prefix-first ref list from
// availableRefs and the current query, then reconciles the highlight: it keeps
// the previously-highlighted ref selected if it survived the filter, otherwise
// it falls back to the current ref (on first open) or the top match.
func (m *Model) refreshRefMatches() {
	_, _, curRef, _ := m.activeRefInfo()
	var prevSel string
	if m.refMatchCursor >= 0 && m.refMatchCursor < len(m.refMatches) {
		prevSel = m.refMatches[m.refMatchCursor]
	}
	m.refMatches = filterRefs(m.availableRefs, m.refInput.Value(), curRef)

	m.refMatchCursor = 0
	target := prevSel
	if target == "" {
		target = curRef
	}
	for i, r := range m.refMatches {
		if r == target {
			m.refMatchCursor = i
			break
		}
	}
}

// filterRefs narrows refs to those matching query by case-insensitive
// substring, ordered prefix-matches-first and preserving the remote's order
// within each group (ADR-0025). An empty query — or a query still equal to the
// seeded current ref (i.e. the field is untouched) — is treated as "browse":
// the whole list is returned so pressing R immediately shows every option.
func filterRefs(refs []string, query, curRef string) []string {
	q := strings.TrimSpace(query)
	if q == "" || q == curRef {
		out := make([]string, len(refs))
		copy(out, refs)
		return out
	}
	lq := strings.ToLower(q)
	var prefix, other []string
	for _, r := range refs {
		idx := strings.Index(strings.ToLower(r), lq)
		switch {
		case idx < 0:
			// no match
		case idx == 0:
			prefix = append(prefix, r)
		default:
			other = append(other, r)
		}
	}
	return append(prefix, other...)
}

// applyRefSwitch merges a successful ref switch result into the model,
// preserving user overrides and recording orphaned variables. The switch is
// applied to the module captured in refModuleIdx (the one the user invoked R
// on), which may be the primary or any secondary module.
func (m *Model) applyRefSwitch(result *RefSwitchResult) {
	m.refSwitching = false
	m.refOrphaned = result.OrphanedVars

	// Refresh the preset picker: the new ref may ship different `.tfvars`
	// example bundles, and the list is otherwise only built at launch.
	m.SetPresets(result.Presets)

	idx := m.refModuleIdx
	if idx < 0 || idx >= len(m.Modules) {
		idx = 0
	}
	entry := &m.Modules[idx]

	// Carry over user overrides for variables that still exist in the new ref.
	// Two kinds of override must survive a ref switch:
	//   1. concrete values   -> entry.State.Values
	//   2. wired expressions  -> entry.State.UnknownAttrs (e.g.
	//      model_uuid = data.juju_model.x.uuid). These do NOT live in Values,
	//      so failing to carry them over silently deletes the reference from
	//      the rendered module block on the next write.
	// Overrides for variables that no longer exist in the new ref are
	// intentionally dropped (reported as orphaned).
	oldState := entry.State
	newState := result.State
	if newState.Values == nil {
		newState.Values = make(map[string]cty.Value)
	}
	newVarNames := make(map[string]bool, len(newState.Vars))
	for _, v := range newState.Vars {
		newVarNames[v.Name] = true
	}
	for name, val := range oldState.Values {
		if newVarNames[name] {
			newState.Values[name] = val
		}
	}
	if len(oldState.UnknownAttrs) > 0 {
		carried := make([]wrapper.RawAttr, 0, len(oldState.UnknownAttrs))
		for _, ra := range oldState.UnknownAttrs {
			if newVarNames[ra.Name] {
				carried = append(carried, ra)
			}
		}
		newState.UnknownAttrs = carried
	}

	// Write the new state back into the owning entry. This is the single
	// source of truth used by writeAllModules — failing to update it here is
	// what previously caused a switched ref to silently revert on the next
	// save.
	entry.State = newState
	entry.Ref = result.LiteralRef
	entry.ResolvedSHA = result.ResolvedSHA

	// Keep the primary alias and legacy header fields coherent.
	if idx == 0 {
		m.State = newState
		m.LiteralRef = result.LiteralRef
		m.ResolvedSHA = result.ResolvedSHA
	}

	// Rebuild the UI.
	m.recomputeRows()

	// When the new ref introduced variables, land the cursor on the first
	// one the user must act on — a new *required* var (no default) takes
	// priority, falling back to the first new var. This turns a breaking
	// API change (e.g. model_uuid -> model) into a guided edit instead of a
	// cryptic "Missing required argument" the user has to hunt for.
	if focus := firstActionableNewVar(result.NewVars); focus != "" {
		m.focusVar(idx, focus)
	}
	m.refreshEditor()
	m.dirty = true

	// Reset planner init state so the next plan re-checks modules.
	if p, ok := m.Planner.(*TfexecPlanner); ok {
		p.ResetInit()
	}

	// Status message — brief one-liner for the footer.
	msg := fmt.Sprintf("Switched %s to ref %s (%s)", entry.Name, result.LiteralRef, shortSHA(result.ResolvedSHA))

	// Build the full multi-line detail for [D] modal.
	var detail strings.Builder
	fmt.Fprintf(&detail, "Module:     %s\n", entry.Name)
	fmt.Fprintf(&detail, "Ref:        %s (%s)\n", result.LiteralRef, shortSHA(result.ResolvedSHA))

	if len(result.OrphanedVars) > 0 {
		fmt.Fprintf(&detail, "\nOrphaned variables (%d):\n", len(result.OrphanedVars))
		for _, name := range result.OrphanedVars {
			fmt.Fprintf(&detail, "  • %s\n", name)
		}
	}
	if len(result.NewVars) > 0 {
		fmt.Fprintf(&detail, "\nNew variables (%d):\n", len(result.NewVars))
		for _, v := range result.NewVars {
			suffix := ""
			if !v.HasDefault {
				suffix = " (required)"
			}
			fmt.Fprintf(&detail, "  • %s%s\n", v.Name, suffix)
		}
	}
	n := requiredUnsetCount(entry.State)
	if n > 0 || result.InitIncomplete {
		fmt.Fprintln(&detail)
		if n > 0 {
			fmt.Fprintf(&detail, "⚠ %d required variables unset\n", n)
		}
		if result.InitIncomplete {
			fmt.Fprintln(&detail, "⚠ terraform init incomplete — re-plan to retry")
		}
	}

	// Build a compact one-line summary. Full details live in the [D] modal.
	summary := []string{}
	if len(result.OrphanedVars) > 0 {
		summary = append(summary, fmt.Sprintf("%d orphaned", len(result.OrphanedVars)))
	}
	if len(result.NewVars) > 0 {
		summary = append(summary, fmt.Sprintf("%d new", len(result.NewVars)))
	}
	if len(summary) > 0 {
		msg += " · " + strings.Join(summary, ", ") + " — see [D] for details"
	}

	// Report the actionable condition as a neutral fact, not a procedure: the
	// [!] markers and the auto-jumped cursor already show the user where to
	// act. A required-unset count explains a non-fatal init failure; if init
	// failed for some other reason, say only that it is incomplete.
	lvl := statusInfo
	if n > 0 {
		msg += fmt.Sprintf(" · %d required unset", n)
		lvl = statusWarn
	} else if result.InitIncomplete {
		msg += " · init incomplete"
		lvl = statusWarn
	}
	m.status = msg
	m.statusLvl = lvl
	m.statusAt = time.Now()

	// Store the full detail for the [D] modal. Only show modal-worthy
	// content when there is something beyond the basic "switched to ref" line.
	detailStr := detail.String()
	if len(result.OrphanedVars) > 0 || len(result.NewVars) > 0 || n > 0 || result.InitIncomplete {
		m.refDetailText = detailStr
	} else {
		m.refDetailText = ""
	}
	m.refDetail = false
}
