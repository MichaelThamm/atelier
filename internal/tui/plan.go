package tui

import (
	"fmt"
	"sort"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
)

// planNodeKind is what a node in the plan tree represents: a module
// boundary, a resource type bucket within that module, or an individual
// resource (the leaf).
type planNodeKind int

const (
	nodeModule planNodeKind = iota
	nodeType
	nodeResource
)

// planNode is one node in the plan tree. The tree has at most three depths
// (module → type → resource) per [ADR-0011].
type planNode struct {
	Kind      planNodeKind
	Label     string
	Children  []*planNode
	Collapsed bool

	// Action is the compact change indicator for resource nodes.
	Action string
	// Change is the source resource_change (only set on resource nodes).
	Change *tfjson.ResourceChange
}

// BuildPlanTree groups a parsed plan's ResourceChanges into a (module →
// type → resource) tree. No-op changes are filtered out (consistent with
// `terraform plan` itself). Public for tests; the TUI doesn't import it from
// outside.
func BuildPlanTree(plan *tfjson.Plan) *planNode {
	root := &planNode{Kind: nodeModule, Label: "<root>"}
	if plan == nil {
		return root
	}

	byModule := map[string][]*tfjson.ResourceChange{}
	var moduleOrder []string
	for _, rc := range plan.ResourceChanges {
		if isNoop(rc) {
			continue
		}
		key := rc.ModuleAddress
		if _, ok := byModule[key]; !ok {
			moduleOrder = append(moduleOrder, key)
		}
		byModule[key] = append(byModule[key], rc)
	}
	sort.Strings(moduleOrder)

	for _, mod := range moduleOrder {
		label := mod
		if label == "" {
			label = "(root)"
		}
		modNode := &planNode{Kind: nodeModule, Label: label}

		byType := map[string][]*tfjson.ResourceChange{}
		var typeOrder []string
		for _, rc := range byModule[mod] {
			if _, ok := byType[rc.Type]; !ok {
				typeOrder = append(typeOrder, rc.Type)
			}
			byType[rc.Type] = append(byType[rc.Type], rc)
		}
		sort.Strings(typeOrder)

		for _, typ := range typeOrder {
			// Default: types are collapsed; modules are expanded.
			// This matches the ADR's "top-level modules expanded, resource
			// types collapsed" default.
			typNode := &planNode{Kind: nodeType, Label: typ, Collapsed: true}
			resourceList := byType[typ]
			sort.SliceStable(resourceList, func(i, j int) bool {
				return resourceList[i].Name < resourceList[j].Name
			})
			for _, rc := range resourceList {
				typNode.Children = append(typNode.Children, &planNode{
					Kind:   nodeResource,
					Label:  resourceLabel(rc),
					Action: actionMarker(rc.Change.Actions),
					Change: rc,
				})
			}
			modNode.Children = append(modNode.Children, typNode)
		}
		root.Children = append(root.Children, modNode)
	}
	return root
}

// resourceLabel is the leaf label: just the resource name, plus its index
// (for_each / count) when present.
func resourceLabel(rc *tfjson.ResourceChange) string {
	if rc == nil {
		return ""
	}
	if rc.Index == nil {
		return rc.Name
	}
	switch idx := rc.Index.(type) {
	case string:
		return fmt.Sprintf("%s[%q]", rc.Name, idx)
	case float64:
		return fmt.Sprintf("%s[%d]", rc.Name, int64(idx))
	}
	return fmt.Sprintf("%s[%v]", rc.Name, rc.Index)
}

func isNoop(rc *tfjson.ResourceChange) bool {
	if rc == nil || rc.Change == nil {
		return true
	}
	return len(rc.Change.Actions) == 1 && rc.Change.Actions[0] == tfjson.ActionNoop
}

// actionMarker maps a Terraform action set to the one-character indicator
// used in the tree leaves.
func actionMarker(actions []tfjson.Action) string {
	switch len(actions) {
	case 0:
		return " "
	case 1:
		switch actions[0] {
		case tfjson.ActionCreate:
			return "+"
		case tfjson.ActionUpdate:
			return "~"
		case tfjson.ActionDelete:
			return "-"
		case tfjson.ActionRead:
			return "R"
		case tfjson.ActionNoop:
			return " "
		}
	case 2:
		// create+delete or delete+create — both denote replace.
		return "↻"
	}
	return "?"
}

// PlanSummary returns the standard one-line counts header.
func PlanSummary(plan *tfjson.Plan) string {
	if plan == nil {
		return "Plan: (no plan)"
	}
	add, change, destroy := 0, 0, 0
	for _, rc := range plan.ResourceChanges {
		if rc.Change == nil {
			continue
		}
		actions := rc.Change.Actions
		if len(actions) == 1 {
			switch actions[0] {
			case tfjson.ActionCreate:
				add++
			case tfjson.ActionUpdate:
				change++
			case tfjson.ActionDelete:
				destroy++
			}
			continue
		}
		if len(actions) == 2 {
			// Replace.
			add++
			destroy++
		}
	}
	return fmt.Sprintf("Plan: %d to add, %d to change, %d to destroy.", add, change, destroy)
}

// planRow is one row in the flattened tree, ready to render.
type planRow struct {
	Node  *planNode
	Depth int
}

// flattenedRows walks the tree in display order, skipping subtrees whose
// parent is collapsed. The root sentinel itself is not emitted.
func flattenedRows(root *planNode) []planRow {
	var out []planRow
	if root == nil {
		return out
	}
	var walk func(n *planNode, depth int)
	walk = func(n *planNode, depth int) {
		if depth > 0 {
			out = append(out, planRow{Node: n, Depth: depth - 1})
		}
		if n.Collapsed {
			return
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return out
}

// nextCursor returns the next valid cursor position (skipping collapsed
// subtrees doesn't apply here because flattenedRows already does that — but
// we still clamp to bounds).
func clampCursor(rows []planRow, cursor int) int {
	if len(rows) == 0 {
		return 0
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= len(rows) {
		return len(rows) - 1
	}
	return cursor
}

// AttributeDiffLine is one rendered diff line in the right-pane attribute
// diff view: a key, an action indicator, and one or two value renderings.
type AttributeDiffLine struct {
	Marker string // "+", "-", "~"
	Key    string
	Before string // empty for create
	After  string // empty for delete
}

// String renders the line per the convention SPEC §7.4 implies.
func (l AttributeDiffLine) String() string {
	switch l.Marker {
	case "+":
		return fmt.Sprintf("+ %s = %s", l.Key, l.After)
	case "-":
		return fmt.Sprintf("- %s = %s", l.Key, l.Before)
	case "~":
		return fmt.Sprintf("~ %s = %s → %s", l.Key, l.Before, l.After)
	}
	return fmt.Sprintf("  %s = %s", l.Key, l.After)
}

// AttributeDiff renders the attribute-level diff for a single resource
// change. Sensitive attributes are masked as "<sensitive>".
//
// Behaviour by action:
//   - create: every After attribute as `+`
//   - update: keys whose values differ → `~`; new keys → `+`; removed keys → `-`
//   - delete: every Before attribute as `-`
//   - replace (delete+create): show like update (forces a clear visual signal)
//   - read / no-op: empty result
func AttributeDiff(rc *tfjson.ResourceChange) []AttributeDiffLine {
	if rc == nil || rc.Change == nil {
		return nil
	}
	change := rc.Change
	before := asMap(change.Before)
	after := asMap(change.After)
	beforeSens := asMap(change.BeforeSensitive)
	afterSens := asMap(change.AfterSensitive)
	afterUnknown := asMap(change.AfterUnknown)

	if len(change.Actions) == 1 {
		switch change.Actions[0] {
		case tfjson.ActionCreate:
			return diffCreate(after, afterSens, afterUnknown)
		case tfjson.ActionDelete:
			return diffDelete(before, beforeSens)
		case tfjson.ActionUpdate:
			return diffUpdate(before, after, beforeSens, afterSens, afterUnknown)
		case tfjson.ActionRead, tfjson.ActionNoop:
			return nil
		}
	}
	if len(change.Actions) == 2 {
		// Replace: render before/after side-by-side with `~`.
		return diffUpdate(before, after, beforeSens, afterSens, afterUnknown)
	}
	return nil
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func diffCreate(after, sens, unknowns map[string]any) []AttributeDiffLine {
	out := make([]AttributeDiffLine, 0, len(after))
	for _, k := range sortedKeys(after) {
		val := renderValue(after[k], isSensitive(sens, k), isUnknown(unknowns, k))
		out = append(out, AttributeDiffLine{Marker: "+", Key: k, After: val})
	}
	// Also include keys that are unknown but not in after (after is nil for unknowns).
	for _, k := range sortedKeys(unknowns) {
		if _, has := after[k]; !has && isUnknown(unknowns, k) {
			out = append(out, AttributeDiffLine{Marker: "+", Key: k, After: "(known after apply)"})
		}
	}
	return out
}

func diffDelete(before, sens map[string]any) []AttributeDiffLine {
	out := make([]AttributeDiffLine, 0, len(before))
	for _, k := range sortedKeys(before) {
		val := renderValue(before[k], isSensitive(sens, k), false)
		out = append(out, AttributeDiffLine{Marker: "-", Key: k, Before: val})
	}
	return out
}

func diffUpdate(before, after, beforeSens, afterSens, afterUnknown map[string]any) []AttributeDiffLine {
	keys := map[string]struct{}{}
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	// Include keys that are unknown (they may not appear in after).
	for k := range afterUnknown {
		if isUnknown(afterUnknown, k) {
			keys[k] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)

	var out []AttributeDiffLine
	for _, k := range ordered {
		bv, hasB := before[k]
		av, hasA := after[k]
		bSens := isSensitive(beforeSens, k)
		aSens := isSensitive(afterSens, k)
		aUnk := isUnknown(afterUnknown, k)
		switch {
		case hasB && !hasA && !aUnk:
			out = append(out, AttributeDiffLine{Marker: "-", Key: k, Before: renderValue(bv, bSens, false)})
		case !hasB && (hasA || aUnk):
			out = append(out, AttributeDiffLine{Marker: "+", Key: k, After: renderValue(av, aSens, aUnk)})
		case aUnk:
			// Value existed before, will change to something unknown after apply.
			out = append(out, AttributeDiffLine{
				Marker: "~",
				Key:    k,
				Before: renderValue(bv, bSens, false),
				After:  "(known after apply)",
			})
		case !valuesEqual(bv, av):
			out = append(out, AttributeDiffLine{
				Marker: "~",
				Key:    k,
				Before: renderValue(bv, bSens, false),
				After:  renderValue(av, aSens, false),
			})
		}
	}
	return out
}

func isSensitive(sens map[string]any, key string) bool {
	if sens == nil {
		return false
	}
	v, ok := sens[key]
	if !ok {
		return false
	}
	// Terraform encodes sensitivity as a boolean (top-level) or as a nested
	// structure mirroring the value (for nested attributes). A nested
	// map/list means some sub-fields are sensitive, but the attribute itself
	// can be shown — only a bare `true` marks the whole value as sensitive.
	t, ok := v.(bool)
	return ok && t
}

// isUnknown checks whether a key is marked as unknown ("known after apply")
// in the AfterUnknown map from the plan JSON.
func isUnknown(unknowns map[string]any, key string) bool {
	if unknowns == nil {
		return false
	}
	v, ok := unknowns[key]
	if !ok {
		return false
	}
	t, ok := v.(bool)
	return ok && t
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// renderValue produces a compact one-line representation of a JSON-decoded
// value. Lists/maps are rendered as their Go fmt; sensitive values are
// masked; unknown values are labelled.
func renderValue(v any, sensitive, unknown bool) string {
	if sensitive {
		return "<sensitive>"
	}
	if unknown {
		return "(known after apply)"
	}
	if v == nil {
		return "null"
	}
	switch t := v.(type) {
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		return fmt.Sprintf("%v", t)
	case float64:
		// Render integers without trailing decimals.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			parts[i] = renderValue(el, false, false)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedKeys(t)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%s", k, renderValue(t[k], false, false)))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprintf("%v", v)
}

// valuesEqual compares two JSON-decoded values structurally. Atelier could
// use reflect.DeepEqual, but the bare implementation makes the intent
// explicit and lets us swap in tighter semantics later (e.g. set-aware
// comparison) without surprise.
func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !valuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, val := range av {
			if !valuesEqual(val, bv[k]) {
				return false
			}
		}
		return true
	}
	return a == b
}
