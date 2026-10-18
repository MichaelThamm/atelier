package juju

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelThamm/atelier/internal/state"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// QueryDefaults supplies the model UUID to the query engine when the target
// already records it. Juju's list resources need a model to enumerate, and the
// wrapper or an existing state file usually names the one being imported; asking
// for it again is the repetition this avoids.
//
// The wrapper is the intended configuration, so it outranks state. The two are
// still checked against each other, and against an explicit --query-var,
// because model_uuid forces replacement: querying one model while planning or
// holding state for another would leave the next apply destroying everything it
// just adopted. A conflict is refused before the query runs.
//
// State may legitimately hold several models (a wrapper can compose modules
// from more than one), so a wrapper's model is accepted whenever state includes
// it. With no wrapper model and several state models, there is no single answer
// to derive: an explicit --query-var chooses.
func (*Provider) QueryDefaults(explicit map[string]string, wrapperState *wrapper.State, dir string) (map[string]string, error) {
	wrapperUUID := ModelUUID(wrapperState)
	stateUUIDs := stateModelUUIDs(dir)
	explicitUUID := explicit["model_uuid"]

	known, source := wrapperUUID, "the wrapper"
	switch {
	case wrapperUUID != "" && len(stateUUIDs) > 0 && !slices.Contains(stateUUIDs, wrapperUUID):
		return nil, fmt.Errorf(
			"model mismatch: the wrapper targets model %s but Terraform state holds resources from %s\n"+
				"  Importing with the wrapper's model would leave the existing state pointing at\n"+
				"  another model. Because model_uuid forces replacement, the next apply would\n"+
				"  destroy those resources. Resolve the mismatch first: restore the wrapper,\n"+
				"  remove the stale state, or import into a different directory.",
			wrapperUUID, strings.Join(stateUUIDs, ", "))
	case wrapperUUID == "" && len(stateUUIDs) == 1:
		known, source = stateUUIDs[0], "Terraform state"
	case wrapperUUID == "" && len(stateUUIDs) > 1 && explicitUUID == "":
		return nil, fmt.Errorf(
			"state holds resources from multiple models: %s\n"+
				"  Import enumerates one model at a time. Pass --query-var model_uuid=<uuid>\n"+
				"  to choose the one to import.",
			strings.Join(stateUUIDs, ", "))
	}

	if explicitUUID != "" && known != "" && explicitUUID != known &&
		!slices.Contains(stateUUIDs, explicitUUID) {
		return nil, fmt.Errorf(
			"model mismatch: --query-var model_uuid names model %s but %s targets model %s\n"+
				"  Importing would enumerate one model while planning another. Because model_uuid\n"+
				"  forces replacement, the next apply would destroy and recreate every imported\n"+
				"  resource. Point both at the same model.",
			explicitUUID, source, known)
	}

	if known == "" {
		return nil, nil
	}
	return map[string]string{"model_uuid": known}, nil
}

// stateModelUUIDs returns the distinct model UUIDs recorded on the deployment's
// applications, sorted. Reading the whole file rather than the first match is
// what makes "state spans several models" a fact the caller can act on instead
// of an arbitrary winner.
func stateModelUUIDs(dir string) []string {
	st, err := state.Read(dir)
	if err != nil || st == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range st.Resources {
		if r.Type != "juju_application" {
			continue
		}
		uuid, _ := r.Attributes["model_uuid"].(string)
		if uuid == "" || seen[uuid] {
			continue
		}
		seen[uuid] = true
		out = append(out, uuid)
	}
	slices.Sort(out)
	return out
}
