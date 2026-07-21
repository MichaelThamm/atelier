package wrapper

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tftypes"
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
// Returns true if a matching variable was found and set.
func (s *State) InjectModelUUID(uuid, name string) bool {
	if uuid == "" || s == nil {
		return false
	}

	// Pattern 1: object variable named "model" with a "uuid" field.
	if v := s.FindVar("model"); v != nil && v.Type != nil &&
		v.Type.Kind == tftypes.KindObject {
		if attr, ok := v.Type.Attributes["uuid"]; ok && attr.Type != nil &&
			attr.Type.Kind == tftypes.KindString {
			// Build the full object value with uuid set and other fields at defaults.
			fields := map[string]cty.Value{}
			for _, fname := range v.Type.AttrOrder {
				a := v.Type.Attributes[fname]
				switch {
				case fname == "uuid":
					fields[fname] = cty.StringVal(uuid)
				case fname == "name" && name != "":
					fields[fname] = cty.StringVal(name)
				default:
					fields[fname] = tftypes.ZeroValue(a.Type)
				}
			}
			s.EnsureValues()
			s.Values["model"] = cty.ObjectVal(fields)
			return true
		}
	}

	// Pattern 2: string variable named "model_uuid".
	if v := s.FindVar("model_uuid"); v != nil && v.Type != nil &&
		v.Type.Kind == tftypes.KindString {
		s.EnsureValues()
		s.Values["model_uuid"] = cty.StringVal(uuid)
		return true
	}

	return false
}
