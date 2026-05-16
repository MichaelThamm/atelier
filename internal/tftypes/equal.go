package tftypes

import "github.com/zclconf/go-cty/cty"

// EqualValue compares two cty values for the purposes of the sparse-write
// rule (ADR-0007). It is intentionally not a generic cty equality:
//
//   - Null on either side compares equal only when both are null. This means
//     "set to null" is treated as differing from "set to []" — but Atelier's
//     UI hides that distinction (SPEC §8.3); callers preparing values for
//     comparison are expected to normalise null↔empty before calling here.
//   - For object values, only fields present on both sides are compared.
//     Fields appearing on only one side are treated as "differs from default"
//     so they are written.
func EqualValue(a, b cty.Value) bool {
	if a.IsNull() && b.IsNull() {
		return true
	}
	if a.IsNull() != b.IsNull() {
		return false
	}
	if !a.IsKnown() || !b.IsKnown() {
		// Unknown values can't be at-default by definition; force a write.
		return false
	}

	ta, tb := a.Type(), b.Type()
	if ta.IsObjectType() && tb.IsObjectType() {
		// Compare by intersection of fields.
		ma := a.AsValueMap()
		mb := b.AsValueMap()
		for name, av := range ma {
			bv, ok := mb[name]
			if !ok {
				return false
			}
			if !EqualValue(av, bv) {
				return false
			}
		}
		for name := range mb {
			if _, ok := ma[name]; !ok {
				return false
			}
		}
		return true
	}
	if ta.IsListType() || ta.IsSetType() || ta.IsTupleType() {
		if a.LengthInt() != b.LengthInt() {
			return false
		}
		la := a.AsValueSlice()
		lb := b.AsValueSlice()
		if ta.IsSetType() {
			// Set equality: each element of a must appear in b.
			for _, av := range la {
				found := false
				for _, bv := range lb {
					if EqualValue(av, bv) {
						found = true
						break
					}
				}
				if !found {
					return false
				}
			}
			return true
		}
		for i := range la {
			if !EqualValue(la[i], lb[i]) {
				return false
			}
		}
		return true
	}
	if ta.IsMapType() {
		ma := a.AsValueMap()
		mb := b.AsValueMap()
		if len(ma) != len(mb) {
			return false
		}
		for k, av := range ma {
			bv, ok := mb[k]
			if !ok {
				return false
			}
			if !EqualValue(av, bv) {
				return false
			}
		}
		return true
	}
	// Primitive: fall back to cty's own RawEquals which is precise.
	return a.RawEquals(b)
}
