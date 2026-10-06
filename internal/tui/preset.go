package tui

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// snapshotValues returns the primary module's current, non-default concrete
// values — exactly what Atelier would write sparsely (wrapper.ShouldEmit +
// SparseValue, ADR-0007). This is the "save a bundle from what you have" path:
// the values are written to a personal `.tfvars` file.
//
// Wired reference expressions (var./module./data./local., preserved in
// UnknownAttrs) are excluded because `.tfvars` holds constants only.
func snapshotValues(s *wrapper.State) map[string]cty.Value {
	raw := make(map[string]bool, len(s.UnknownAttrs))
	for _, ra := range s.UnknownAttrs {
		raw[ra.Name] = true
	}

	out := make(map[string]cty.Value)
	for i := range s.Vars {
		v := &s.Vars[i]
		if raw[v.Name] {
			continue
		}
		current, _ := s.VariableValue(v.Name)
		if current == cty.NilVal {
			continue // required-but-unset: nothing concrete to capture
		}
		if !wrapper.ShouldEmit(v, current) {
			continue // at its default — omit, matching the sparse rule
		}
		writeVal := wrapper.SparseValue(v, current)
		if writeVal == cty.NilVal {
			continue
		}
		out[v.Name] = writeVal
	}
	return out
}
