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
}

// MatchedImport pairs a module address with the live object's resource type
// and display name, enough to construct a provider-specific import ID.
type MatchedImport struct {
	Address      string         // module address to import into
	ResourceType string         // e.g. "juju_application"
	Name         string         // live object's display name (e.g. "alertmanager")
	Identity     map[string]any // live object's provider identity (e.g. {"id": "uuid:app1:ep1:app2:ep2"})
}

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
func Match(live []tfexec.LiveResource, planned []PlannedResource, verbose bool) (matched []MatchedImport, unmatchedPlanned []PlannedResource, unmatchedLive []tfexec.LiveResource) {
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
		candidates := candidateIndexes(p.Type, targetName, p.PlannedName, p.Identity, p.PlannedAttrs, live, used, verbose)
		if verbose {
			fmt.Fprintf(os.Stderr, "  -> %d candidates\n", len(candidates))
		}
		if len(candidates) != 1 {
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
//  3. Attribute-based match for integrations: match by application + endpoint
//     pair when the planned and live resources share those attributes.
//
// Later phases run only when earlier phases yield zero or multiple candidates.
func candidateIndexes(resourceType, targetName, plannedName string,
	plannedIdentity, plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool, verbose bool) []int {

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
	}

	// Phase 3: attribute-based match for Juju integration and offer resources.
	//
	// juju_integration: identity is { "id": "<uuid>:<app1>:<ep1>:<app2>:<ep2>" }
	// and "application" is a SetNestedBlock (not a simple attribute). AfterIdentity
	// is nil for planned creates (identity is computed). Match by extracting
	// (app_name, endpoint) pairs from the planned After.application nested block
	// and comparing with the parsed identity from the live resource. Both names
	// AND endpoints must match to disambiguate resources with the same apps but
	// different endpoints (e.g. grafana_dashboards vs grafana_sources).
	//
	// juju_offer: identity is { "id": "<offer_url>" }. Match by application_name
	// from the planned After against the live display name or parsed identity.
	if len(plannedAttrs) > 0 {
		if resourceType == "juju_integration" {
			plannedPairs := extractIntegrationEndpointPairs(plannedAttrs)
			if verbose {
				fmt.Fprintf(os.Stderr, "  [integration] plannedPairs=%v\n", plannedPairs)
			}
			if len(plannedPairs) > 0 {
				var out []int
				for i, lr := range live {
					if used[i] || lr.ResourceType != resourceType {
						continue
					}
					livePairs := parseIntegrationIDEndpointPairs(lr.Identity)
					if len(livePairs) > 0 && endpointPairSetEqual(plannedPairs, livePairs) {
						out = append(out, i)
					}
				}
				if len(out) == 1 {
					return out
				}
			}
		}
		if resourceType == "juju_offer" {
			pAppName, _ := plannedAttrs["application_name"].(string)
			pName, _ := plannedAttrs["name"].(string)
			if verbose {
				fmt.Fprintf(os.Stderr, "  [offer] pAppName=%q pName=%q plannedAttrs=%v\n", pAppName, pName, plannedAttrs)
			}
			if pName != "" || pAppName != "" {
				// Phase 3a: match by offer name (exact, from planned "name" attribute).
				if pName != "" {
					var out []int
					for i, lr := range live {
						if used[i] || lr.ResourceType != resourceType {
							continue
						}
						liveURL := offerURLFromIdentity(lr.Identity)
						if liveURL == "" {
							continue
						}
						liveOfferName := liveURL
						if dot := strings.LastIndex(liveURL, "."); dot >= 0 {
							liveOfferName = liveURL[dot+1:]
						}
						if verbose {
							fmt.Fprintf(os.Stderr, "  [offer]   live[%d] %s liveOfferName=%q pName=%q match=%v\n", i, lr.DisplayName, liveOfferName, pName, liveOfferName == pName)
						}
						if liveOfferName == pName {
							out = append(out, i)
						}
					}
					if verbose {
						fmt.Fprintf(os.Stderr, "  [offer] phase3a (pName) -> %d candidates\n", len(out))
					}
					if len(out) == 1 {
						return out
					}
				}
				// Phase 3b: match by application_name containment in URL.
				if pAppName != "" {
					var out []int
					for i, lr := range live {
						if used[i] || lr.ResourceType != resourceType {
							continue
						}
						liveURL := offerURLFromIdentity(lr.Identity)
						if liveURL == "" {
							continue
						}
						if verbose {
							fmt.Fprintf(os.Stderr, "  [offer]   live[%d] %s url=%q containsAppName=%v\n", i, lr.DisplayName, liveURL, strings.Contains(liveURL, pAppName))
						}
						if strings.Contains(liveURL, pAppName) {
							out = append(out, i)
						}
					}
					if verbose {
						fmt.Fprintf(os.Stderr, "  [offer] phase3b (pAppName) -> %d candidates\n", len(out))
					}
					if len(out) == 1 {
						return out
					}
				}
			}
		}
	}

	return nil
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
// dot-separated segment. E.g. "module.cos.juju_application.alertmanager" →
// "alertmanager".
func shortName(addr string) string {
	if i := strings.LastIndex(addr, "."); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// extractIntegrationEndpointPairs extracts (app_name, endpoint) pairs from the
// planned After attributes of a juju_integration resource. The "application"
// attribute is a SetNestedBlock containing objects with "name" and "endpoint"
// fields. Returns a sorted slice of "name:endpoint" strings.
func extractIntegrationEndpointPairs(attrs map[string]any) []string {
	raw, ok := attrs["application"]
	if !ok {
		return nil
	}
	apps, ok := raw.([]any)
	if !ok {
		return nil
	}
	var pairs []string
	for _, a := range apps {
		m, ok := a.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		endpoint, _ := m["endpoint"].(string)
		if name != "" && endpoint != "" {
			pairs = append(pairs, name+":"+endpoint)
		}
	}
	sort.Strings(pairs)
	return pairs
}

// parseIntegrationIDEndpointPairs parses the identity "id" string of a
// juju_integration to extract (app_name, endpoint) pairs. The ID format is:
//
//	<model_uuid>:<provider_app>:<provider_endpoint>:<requirer_app>:<requirer_endpoint>
//
// Returns a sorted slice of "name:endpoint" strings.
func parseIntegrationIDEndpointPairs(identity map[string]any) []string {
	if identity == nil {
		return nil
	}
	idStr, ok := identity["id"].(string)
	if !ok {
		return nil
	}
	parts := strings.Split(idStr, ":")
	if len(parts) != 5 {
		return nil
	}
	pairs := []string{parts[1] + ":" + parts[2], parts[3] + ":" + parts[4]}
	sort.Strings(pairs)
	return pairs
}

// endpointPairSetEqual checks if two sorted string slices contain the same
// elements. Each element is a "name:endpoint" pair.
func endpointPairSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// offerURLFromIdentity extracts the "id" value from a juju_offer identity map.
// The identity ID is the offer URL (e.g. "admin/model.foobar:my-offer").
func offerURLFromIdentity(identity map[string]any) string {
	if identity == nil {
		return ""
	}
	id, _ := identity["id"].(string)
	return id
}
