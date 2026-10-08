package importer

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// PlannedResource is a resource the target module wants to create (present in
// config, absent from state) — i.e. an import candidate.
type PlannedResource struct {
	// Address is the full module address, e.g.
	// "module.cos.juju_application.alertmanager".
	Address string
	// Type is the resource type, e.g. "juju_application".
	Type string
	// Module is the module address the resource belongs to ("" for the root
	// module), taken from the plan. Reports group unmatched resources by it so
	// a whole absent subtree reads as one line instead of dozens.
	Module string
	// PlannedName is the value of the "name" attribute in the planned state
	// (from After). When the Terraform resource label differs from the
	// provider object's display name (e.g. juju_application "self-signed-certificates"
	// with name = "ca"), this provides the correct match key.
	PlannedName string
	// Identity is the provider-declared resource identity from the plan's
	// AfterIdentity (TF 1.14+). When present, matching uses identity first
	// (exact match on all shared keys) before falling back to name-based
	// matching.
	Identity map[string]any
	// PlannedAttrs holds all attributes from the plan's After value, used
	// for attribute-based matching (e.g. integrations matched by endpoint
	// pair).
	PlannedAttrs map[string]any

	// The two fields below are not derived from the plan; the pipeline fills
	// them on unmatched resources so a report can say what, if anything, a
	// user must do about each.

	// Create reports whether the plan would create this resource (it is
	// absent from state). False means it is already tracked, so an unmatched
	// entry needs no action even though no live object matched it.
	Create bool
	// LiveCandidates is the number of live objects the matcher found, set only
	// on unmatched resources: 0 means nothing live exists (nothing to import),
	// >1 means the match was ambiguous and needs a manual decision.
	LiveCandidates int
}

// MatchedImport pairs a module address with the live object's resource type
// and display name, enough to construct a provider-specific import ID.
type MatchedImport struct {
	Address      string         // module address to import into
	ResourceType string         // e.g. "juju_application"
	Name         string         // live object's display name (e.g. "alertmanager")
	Identity     map[string]any // live object's provider identity (e.g. {"id": "uuid:app1:ep1:app2:ep2"})
}

// FallbackMatcher lets a provider add matching rules for resource types the
// generic identity/name phases cannot resolve. It runs only after those phases
// yield zero or multiple candidates, and returns the indexes of unused live
// objects that match. The caller treats a single index as a match and any
// other count (including zero) as unresolved, so a fallback should return every
// candidate it finds rather than collapsing an ambiguous result to nil — that
// keeps the ambiguity visible to the verbose trace.
//
// It exists because some resources are keyed by provider-internal composite
// identities — Juju integrations by endpoint pairs, offers by URL — which the
// core must not hardcode. PlannedAttrs is the plan's After value; live is the
// full live set; used marks live objects already consumed this run.
type FallbackMatcher func(resourceType, targetName, plannedName string, plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool, verbose bool) []int

// unimportableTypes lists resource types that have no live counterpart and
// can never be imported. These are Terraform core / internal resources (e.g.
// terraform_data used for replace_triggers, computed interfaces, etc.) that
// exist only in Terraform state. Filtering them from PlannedCreates avoids
// false "unmatched" reports — they will be created on the first terraform
// apply after import.
var unimportableTypes = map[string]bool{
	"terraform_data": true,
}

// PlannedCreates extracts the resources a plan would create — the import
// candidates. Resource types that have no live counterpart (see
// unimportableTypes) are excluded.
//
// When includeExisting is true, resources already present in state (no-op
// changes) are also included. This gives the caller the full set of module
// resources for matching live objects against module addresses, even when
// the state already tracks some of them.
func PlannedCreates(plan *tfjson.Plan, includeExisting bool) []PlannedResource {
	if plan == nil {
		return nil
	}
	var out []PlannedResource
	for _, rc := range plan.ResourceChanges {
		if rc == nil || rc.Change == nil {
			continue
		}
		if rc.Change.Importing != nil {
			continue
		}
		if !rc.Change.Actions.Create() && !includeExisting {
			continue
		}
		if unimportableTypes[rc.Type] {
			continue
		}
		// When includeExisting is false, skip no-op (already-in-state) resources.
		if !includeExisting && rc.Change.Actions.NoOp() {
			continue
		}
		pr := PlannedResource{
			Address: rc.Address,
			Type:    rc.Type,
			Module:  rc.ModuleAddress,
		}
		if after, ok := rc.Change.After.(map[string]any); ok {
			pr.PlannedAttrs = after
			if name, ok := after["name"].(string); ok {
				pr.PlannedName = name
			}
		}
		if afterID, ok := rc.Change.AfterIdentity.(map[string]any); ok {
			pr.Identity = afterID
		}
		out = append(out, pr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// Match pairs each planned create with exactly one live object of the same
// resource type whose display name matches the planned resource's short name
// (the last dot-separated segment of its module address).
//
// A planned resource is matched only when exactly one unused live object
// qualifies; zero or multiple candidates leave it unmatched (reported so the
// user can resolve it). Each live object is consumed by at most one planned
// resource.
func Match(live []tfexec.LiveResource, planned []PlannedResource, fallback FallbackMatcher, verbose bool) (matched []MatchedImport, unmatchedPlanned []PlannedResource, unmatchedLive []tfexec.LiveResource) {
	used := make([]bool, len(live))

	if verbose {
		fmt.Fprintf(os.Stderr, "\n[match] %d planned, %d live\n", len(planned), len(live))
		for i, lr := range live {
			fmt.Fprintf(os.Stderr, "  live[%d]: %s/%s identity=%v\n", i, lr.ResourceType, lr.DisplayName, lr.Identity)
		}
	}

	for _, p := range planned {
		targetName := shortName(p.Address)
		if verbose {
			fmt.Fprintf(os.Stderr, "\n[match] planned: %s type=%s targetName=%q plannedName=%q identity=%v\n",
				p.Address, p.Type, targetName, p.PlannedName, p.Identity)
		}
		candidates := candidateIndexes(p.Type, targetName, p.PlannedName, p.Identity, p.PlannedAttrs, live, used, fallback, verbose)
		if verbose {
			fmt.Fprintf(os.Stderr, "  -> %d candidates\n", len(candidates))
		}
		if len(candidates) != 1 {
			// Record how many live objects qualified, so a report can tell "no
			// live counterpart" (0, nothing to import) apart from "ambiguous"
			// (>1, needs a manual decision). Both stay unmatched.
			p.LiveCandidates = len(candidates)
			unmatchedPlanned = append(unmatchedPlanned, p)
			continue
		}
		idx := candidates[0]
		used[idx] = true
		matched = append(matched, MatchedImport{
			Address:      p.Address,
			ResourceType: p.Type,
			Name:         live[idx].DisplayName,
			Identity:     live[idx].Identity,
		})
	}

	for i, lr := range live {
		if !used[i] {
			unmatchedLive = append(unmatchedLive, lr)
		}
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "\n[match] result: %d matched, %d unmatched planned, %d unmatched live\n",
			len(matched), len(unmatchedPlanned), len(unmatchedLive))
		for _, m := range matched {
			fmt.Fprintf(os.Stderr, "  matched: %s -> %s/%s\n", m.Address, m.ResourceType, m.Name)
		}
		for _, p := range unmatchedPlanned {
			fmt.Fprintf(os.Stderr, "  unmatched planned: %s (type=%s)\n", p.Address, p.Type)
		}
	}
	return matched, unmatchedPlanned, unmatchedLive
}

// candidateIndexes returns the indexes of unused live objects that match the
// given resource type. Matching uses a three-phase strategy:
//
//  1. Identity match (preferred): when the planned resource has a provider-
//     declared identity (TF 1.14+), find live objects whose identity matches
//     on all shared keys. If exactly one matches, use it.
//  2. Name-based fallback: match by display name against the Terraform
//     resource label (targetName) or the planned attribute name (plannedName).
//  3. Provider fallback: when a FallbackMatcher is supplied, it gets a chance
//     to resolve types the generic phases cannot (e.g. Juju integrations keyed
//     by endpoint pairs, offers by URL).
//
// Later phases run only when earlier phases yield zero or multiple candidates.
// The return value is the decisive singleton on a match. When nothing resolves
// to exactly one, it is the largest set of candidates any phase produced — nil
// for "no live counterpart", non-nil for "ambiguous" — which lets a report
// tell the two apart. A caller must treat any non-singleton result as a miss.
func candidateIndexes(resourceType, targetName, plannedName string,
	plannedIdentity, plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool, fallback FallbackMatcher, verbose bool) []int {

	var ambiguous []int

	// Phase 1: exact identity match (provider-declared, preferred).
	if len(plannedIdentity) > 0 {
		var out []int
		for i, lr := range live {
			if used[i] || lr.ResourceType != resourceType {
				continue
			}
			if identityMatch(plannedIdentity, lr.Identity) {
				out = append(out, i)
			}
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "  [phase1] type=%s identity=%v -> %d candidates\n", resourceType, plannedIdentity, len(out))
		}
		if len(out) == 1 {
			return out
		}
		if len(out) > 1 {
			ambiguous = out
		}
	}

	// Phase 2: name-based match (existing heuristic).
	{
		var out []int
		for i, lr := range live {
			if used[i] || lr.ResourceType != resourceType {
				continue
			}
			if lr.DisplayName == targetName || (plannedName != "" && lr.DisplayName == plannedName) {
				out = append(out, i)
			}
		}
		if verbose {
			fmt.Fprintf(os.Stderr, "  [phase2] type=%s target=%q planned=%q -> %d candidates\n", resourceType, targetName, plannedName, len(out))
		}
		if len(out) == 1 {
			return out
		}
		if len(out) > 1 {
			ambiguous = out
		}
	}

	// Phase 3: provider-specific fallback, if the selected provider offers one.
	if fallback != nil {
		out := fallback(resourceType, targetName, plannedName, plannedAttrs, live, used, verbose)
		if len(out) == 1 {
			return out
		}
		if len(out) > 1 {
			ambiguous = out
		}
	}

	return ambiguous
}

// identityMatch checks whether two identity objects are compatible. All keys
// present in the planned identity must appear in the live identity with equal
// values. Extra keys in the live identity are ignored (the plan may declare a
// subset). Both maps must be non-nil. Values are compared with ==; numeric
// types from JSON (float64) are compared directly.
func identityMatch(planned, live map[string]any) bool {
	if len(planned) == 0 || len(live) == 0 {
		return false
	}
	for k, pv := range planned {
		lv, ok := live[k]
		if !ok {
			return false
		}
		if pv != lv {
			return false
		}
	}
	return true
}

// shortName extracts the resource label from a module address — the last
// dot-separated segment, minus any count index or for_each key. E.g.
// "module.cos.juju_application.alertmanager" → "alertmanager" and
// `module.cos.juju_model.cos[0]` → "cos". Without stripping the index, a
// count-indexed address never matches a live display name, so an existing
// resource is reported as unmatched.
func shortName(addr string) string {
	// Strip a trailing count index (`[0]`) or for_each key (`["a.b"]`) before
	// segmenting: the key may itself contain a dot, so splitting first would
	// cut inside it.
	if i := strings.LastIndexByte(addr, '['); i >= 0 && strings.HasSuffix(addr, "]") {
		addr = addr[:i]
	}
	if i := strings.LastIndexByte(addr, '.'); i >= 0 {
		addr = addr[i+1:]
	}
	return addr
}
