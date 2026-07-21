package state

// ExtractModelUUID returns the first model UUID found in juju_application
// resources in the state, or "" if none. When multiple applications exist in
// the same model (as is typical), they all share the same UUID, so any one
// suffices.
func (s *State) ExtractModelUUID() string {
	if s == nil {
		return ""
	}
	for _, r := range s.Resources {
		if r.Type == "juju_application" {
			if v, ok := r.Attributes["model_uuid"]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// ExtractModelName returns the name of the first juju_model resource found in
// the state, or "" if none. This is used to populate the model variable's
// name field in the wrapper.
func (s *State) ExtractModelName() string {
	if s == nil {
		return ""
	}
	for _, r := range s.Resources {
		if r.Type == "juju_model" {
			if v, ok := r.Attributes["name"]; ok {
				if s, ok := v.(string); ok && s != "" {
					return s
				}
			}
		}
	}
	return ""
}
