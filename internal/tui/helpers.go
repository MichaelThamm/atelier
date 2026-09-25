package tui

import (
	"fmt"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
)

// requiredUnsetCount returns how many of the module's variables are required
// (no default) but have neither a concrete value nor a wired expression — the
// same condition the [!] marker reports per row.
func requiredUnsetCount(st *wrapper.State) int {
	if st == nil {
		return 0
	}
	n := 0
	for _, v := range st.Vars {
		if v.HasDefault {
			continue
		}
		if _, wired := st.WiredExpression(v.Name); wired {
			continue
		}
		cur, present := st.Values[v.Name]
		if !present || cur == cty.NilVal {
			n++
		}
	}
	return n
}

// firstActionableNewVar returns the name of the variable the user should be
// taken to after a ref switch: the first new required variable (no default),
// or the first new variable if all have defaults, or "" if there are none.
func firstActionableNewVar(newVars []tfvars.Variable) string {
	if len(newVars) == 0 {
		return ""
	}
	for _, v := range newVars {
		if !v.HasDefault {
			return v.Name
		}
	}
	return newVars[0].Name
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// writeAllModules writes the state for every module in the session.
// Each module's State.Write() is idempotent on its own block in main.tf.
func writeAllModules(modules []ModuleEntry) error {
	for _, mod := range modules {
		if mod.State != nil {
			if err := mod.State.Write(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Format helper: short SHA.
func shortSHA(sha string) string {
	if len(sha) >= 7 {
		return sha[:7]
	}
	return sha
}

// moduleLabel renders a module's display name with its git-ref pin, if any.
// Unpinned modules render as the bare name (no SHA, no synthesized branch).
func moduleLabel(name, ref string) string {
	if ref == "" {
		return name
	}
	return name + "@" + ref
}

// unpinnedMarker is the dim, non-ref affordance shown beside a remote module
// that carries no git pin. It is a word (never a sigil or sha-like token) so
// it cannot be mistaken for a ref, and it is only ever shown for pinnable
// (remote) modules — local sources have nothing to pin (ADR-0019 amendment).
const unpinnedMarker = "·unpinned"

// stub used by view: format kind label briefly.
func kindLabel(t *tftypes.Type) string {
	if t == nil {
		return "any"
	}
	return t.Kind.String()
}

// Module-info banner used by the status line. Single-module sessions render
// "Module: <token>"; multi-module sessions add the active module's position
// ("Module 2/3: …") so the banner carries information the per-group section
// headers cannot. Unpinned remote modules get a dim "unpinned" affordance;
// local (unpinnable) modules render as a bare name (ADR-0019).
func (m *Model) moduleBanner() string {
	name, source, ref, _ := m.activeRefInfo()
	if name == "" {
		return ""
	}
	prefix := "Module: "
	if n := len(m.Modules); n > 1 {
		prefix = fmt.Sprintf("Module %d/%d: ", m.activeModuleIdx()+1, n)
	}
	label := moduleLabel(name, ref)
	if ref == "" && source != "" {
		label += " " + styleUnpinnedTag.Render(unpinnedMarker)
	}
	return prefix + label
}

// formatValidateDiagnostics renders validate diagnostics into a multi-line
// string suitable for the status bar and logs view.
func formatValidateDiagnostics(vo *tfjson.ValidateOutput) string {
	if vo == nil || len(vo.Diagnostics) == 0 {
		return ""
	}
	var b strings.Builder
	for i, d := range vo.Diagnostics {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		sev := "Error"
		if d.Severity == "warning" {
			sev = "Warning"
		}
		fmt.Fprintf(&b, "%s: %s", sev, d.Summary)
		if d.Detail != "" {
			fmt.Fprintf(&b, "\n  %s", d.Detail)
		}
	}
	return b.String()
}

// formatCheckWarnings renders failed `check` block assertions into a
// multi-line string for the [W] warnings detail modal: each warning shows the
// check's display address followed by the author's error_message, indented.
func formatCheckWarnings(warnings []CheckWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	var b strings.Builder
	for i, w := range warnings {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		fmt.Fprintf(&b, "Warning: %s", w.Address)
		if w.Message != "" {
			fmt.Fprintf(&b, "\n  %s", w.Message)
		}
	}
	return b.String()
}
