package wrapper

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
)

// ModelUUIDResult reports the outcome of InjectModelUUID. The three cases are
// kept distinct because they demand different handling: an injection is worth
// reporting, an already-set value is expected and silent, and a miss is a
// problem the user needs to hear about.
type ModelUUIDResult int

const (
	// ModelUUIDNoVariable means no variable matched a known model-UUID
	// convention, so nothing was written. Callers should warn: the plan will be
	// computed with the UUID unset.
	ModelUUIDNoVariable ModelUUIDResult = iota
	// ModelUUIDAlreadySet means the user had already chosen a model. Nothing
	// was changed and nothing needs saying.
	ModelUUIDAlreadySet
	// ModelUUIDInjected means a value was written; the wrapper needs saving.
	ModelUUIDInjected
)

// InjectModelUUID sets the model UUID (and optionally the model name) on the
// wrapper's variables so that terraform plan sees a concrete model_uuid
// instead of "(known after apply)".
//
// This prevents the RequiresReplace cascade on juju_application resources:
// without a concrete model_uuid, Terraform computes it from a not-yet-created
// juju_model resource, triggering destroy-and-recreate for every application.
//
// The function searches for two common variable patterns in Juju modules:
//  1. A variable named "model" with an object type containing a "uuid" field
//     (used by COS-Lite and similar modules: `model = { uuid = "..." }`).
//  2. A variable named "model_uuid" of string type (direct variable).
//
// A UUID the user has already set is never overwritten, and for the object
// pattern any other fields the user set are preserved — this may run against a
// wrapper the user has already edited.
//
// Note that this is convention matching, not inference: the variable names are
// matched literally. A module that carries the model UUID under some other name
// yields ModelUUIDNoVariable, which callers must surface — silently planning
// with the UUID unset is what produces wrong resource addresses in modules that
// branch on it.
func (s *State) InjectModelUUID(uuid, name string) ModelUUIDResult {
	if uuid == "" || s == nil {
		return ModelUUIDNoVariable
	}

	// Pattern 1: object variable named "model" with a "uuid" field.
	if v := s.FindVar("model"); v != nil && v.Type != nil &&
		v.Type.Kind == tftypes.KindObject {
		if attr, ok := v.Type.Attributes["uuid"]; ok && attr.Type != nil &&
			attr.Type.Kind == tftypes.KindString {
			// Start from whatever the user already has so their other fields
			// survive; fall back to per-field defaults for the rest. The
			// sparse writer prunes at-default fields on the way out, so this
			// still renders as a minimal `model = { uuid = "..." }`.
			existing := map[string]cty.Value{}
			if cur, ok := s.Values["model"]; ok && cur != cty.NilVal &&
				!cur.IsNull() && cur.Type().IsObjectType() {
				existing = cur.AsValueMap()
			}
			if isNonEmptyString(existing["uuid"]) {
				return ModelUUIDAlreadySet // user already chose a model
			}

			fields := map[string]cty.Value{}
			for _, fname := range v.Type.AttrOrder {
				a := v.Type.Attributes[fname]
				switch {
				case fname == "uuid":
					fields[fname] = cty.StringVal(uuid)
				case existing[fname] != cty.NilVal:
					fields[fname] = existing[fname]
				case fname == "name" && name != "":
					fields[fname] = cty.StringVal(name)
				case a.HasDefault:
					// Use the field's own declared default rather than the
					// type's zero value, so the sparse writer recognises it as
					// at-default and prunes it. Emitting a zero value instead
					// writes noise the user never asked for (e.g. `name = ""`
					// for a field declared `optional(string, "cos-lite")`), and
					// can trip module validation that requires it non-empty.
					fields[fname] = a.Default
				default:
					fields[fname] = tftypes.ZeroValue(a.Type)
				}
			}
			s.EnsureValues()
			s.Values["model"] = cty.ObjectVal(fields)
			return ModelUUIDInjected
		}
	}

	// Pattern 2: string variable named "model_uuid".
	if v := s.FindVar("model_uuid"); v != nil && v.Type != nil &&
		v.Type.Kind == tftypes.KindString {
		if isNonEmptyString(s.Values["model_uuid"]) {
			return ModelUUIDAlreadySet
		}
		s.EnsureValues()
		s.Values["model_uuid"] = cty.StringVal(uuid)
		return ModelUUIDInjected
	}

	return ModelUUIDNoVariable
}

// isNonEmptyString reports whether v is a known, non-null, non-empty string.
func isNonEmptyString(v cty.Value) bool {
	return v != cty.NilVal && !v.IsNull() && v.Type() == cty.String &&
		v.IsKnown() && v.AsString() != ""
}

// ModelUUID returns the model UUID currently declared in the wrapper under
// either recognised convention, or "" when none is set.
//
// Callers use this to detect a wrapper that targets a different model than the
// one being imported. That mismatch is not cosmetic: model_uuid forces
// replacement on Juju resources, so applying it would destroy every imported
// resource and recreate it in the other model.
func (s *State) ModelUUID() string {
	if s == nil {
		return ""
	}
	if v := s.FindVar("model"); v != nil && v.Type != nil &&
		v.Type.Kind == tftypes.KindObject {
		if cur, ok := s.Values["model"]; ok && cur != cty.NilVal &&
			!cur.IsNull() && cur.Type().IsObjectType() {
			if u, ok := cur.AsValueMap()["uuid"]; ok && isNonEmptyString(u) {
				return u.AsString()
			}
		}
	}
	if v := s.FindVar("model_uuid"); v != nil && isNonEmptyString(s.Values["model_uuid"]) {
		return s.Values["model_uuid"].AsString()
	}
	return ""
}
