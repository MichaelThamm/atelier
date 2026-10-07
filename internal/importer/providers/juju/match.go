package juju

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// MatchFallback resolves the Juju resource types the generic identity/name
// phases cannot: integrations (keyed by endpoint pairs, not a simple name),
// offers (keyed by a URL), and models (whose live display name is the UUID, not
// the model name). It runs only after the generic phases are inconclusive. The
// importer core never needs to know these formats.
func (*Provider) MatchFallback() importer.FallbackMatcher { return matchFallback }

// matchFallback implements importer.FallbackMatcher for Juju.
//
// juju_integration: identity is { "id": "<uuid>:<app1>:<ep1>:<app2>:<ep2>" }
// and "application" is a SetNestedBlock (not a simple attribute). AfterIdentity
// is nil for planned creates (identity is computed), so match by extracting
// (app_name, endpoint) pairs from the planned After.application nested block
// and comparing with the parsed identity from the live resource. Both names
// AND endpoints must match to disambiguate resources with the same apps but
// different endpoints (e.g. grafana_dashboards vs grafana_sources).
//
// juju_offer: identity is { "id": "<offer_url>" }. Match by the planned "name"
// against the offer name parsed from the live URL, then by application_name
// containment in the URL as a fallback.
//
// juju_model: the live identity is the model UUID, and the query's display name
// for it is the UUID too, so the generic name phase can never connect it to a
// plan whose "name" is the model name. Match on the planned UUID (an already
// managed model) and fall back to the planned name against the live
// resource_object's "name".
func matchFallback(resourceType, targetName, plannedName string, plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool, verbose bool) []int {
	if len(plannedAttrs) == 0 {
		return nil
	}
	switch resourceType {
	case "juju_integration":
		return matchIntegration(plannedAttrs, live, used, verbose)
	case "juju_offer":
		return matchOffer(plannedAttrs, live, used, verbose)
	case "juju_model":
		return matchModel(plannedAttrs, live, used)
	}
	return nil
}

func matchIntegration(plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool, verbose bool) []int {
	plannedPairs := extractIntegrationEndpointPairs(plannedAttrs)
	if verbose {
		fmt.Fprintf(os.Stderr, "  [integration] plannedPairs=%v\n", plannedPairs)
	}
	if len(plannedPairs) == 0 {
		return nil
	}
	var out []int
	for i, lr := range live {
		if used[i] || lr.ResourceType != "juju_integration" {
			continue
		}
		livePairs := parseIntegrationIDEndpointPairs(lr.Identity)
		if len(livePairs) > 0 && endpointPairSetEqual(plannedPairs, livePairs) {
			out = append(out, i)
		}
	}
	return out
}

func matchOffer(plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool, verbose bool) []int {
	pAppName, _ := plannedAttrs["application_name"].(string)
	pName, _ := plannedAttrs["name"].(string)
	if verbose {
		fmt.Fprintf(os.Stderr, "  [offer] pAppName=%q pName=%q\n", pAppName, pName)
	}
	if pName == "" && pAppName == "" {
		return nil
	}

	// Match by offer name first (exact, from the planned "name" attribute).
	if pName != "" {
		var out []int
		for i, lr := range live {
			if used[i] || lr.ResourceType != "juju_offer" {
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
			if liveOfferName == pName {
				out = append(out, i)
			}
		}
		if len(out) == 1 {
			return out
		}
	}

	// Fall back to application_name containment in the URL.
	var out []int
	for i, lr := range live {
		if used[i] || lr.ResourceType != "juju_offer" {
			continue
		}
		liveURL := offerURLFromIdentity(lr.Identity)
		if liveURL == "" {
			continue
		}
		if pAppName != "" && strings.Contains(liveURL, pAppName) {
			out = append(out, i)
		}
	}
	return out
}

// matchModel resolves a juju_model. The live object's identity and display name
// are both the model UUID, while the plan carries the model *name*, so the
// generic name phase finds nothing. Prefer the planned UUID (present for a
// model already in state) and fall back to the planned name matched against the
// live resource_object's "name" (present on a create, where the UUID is not yet
// known).
func matchModel(plannedAttrs map[string]any, live []tfexec.LiveResource, used []bool) []int {
	uuid, _ := plannedAttrs["uuid"].(string)
	if uuid == "" {
		uuid, _ = plannedAttrs["id"].(string)
	}
	name, _ := plannedAttrs["name"].(string)
	if uuid == "" && name == "" {
		return nil
	}
	var out []int
	for i, lr := range live {
		if used[i] || lr.ResourceType != "juju_model" {
			continue
		}
		if uuid != "" {
			if id, _ := lr.Identity["id"].(string); id == uuid {
				out = append(out, i)
				continue
			}
			if u, _ := lr.Attributes["uuid"].(string); u == uuid {
				out = append(out, i)
				continue
			}
		}
		if name != "" {
			if n, _ := lr.Attributes["name"].(string); n == name {
				out = append(out, i)
			}
		}
	}
	return out
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
