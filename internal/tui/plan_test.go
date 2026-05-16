package tui

import (
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

// Sample plan helpers --------------------------------------------------------

func change(actions ...tfjson.Action) *tfjson.Change {
	return &tfjson.Change{Actions: actions}
}

func rc(addr, modAddr, typ, name string, ch *tfjson.Change) *tfjson.ResourceChange {
	return &tfjson.ResourceChange{
		Address:       addr,
		ModuleAddress: modAddr,
		Type:          typ,
		Name:          name,
		Change:        ch,
	}
}

func TestActionMarker(t *testing.T) {
	cases := []struct {
		in   []tfjson.Action
		want string
	}{
		{[]tfjson.Action{tfjson.ActionCreate}, "+"},
		{[]tfjson.Action{tfjson.ActionUpdate}, "~"},
		{[]tfjson.Action{tfjson.ActionDelete}, "-"},
		{[]tfjson.Action{tfjson.ActionRead}, "R"},
		{[]tfjson.Action{tfjson.ActionNoop}, " "},
		{[]tfjson.Action{tfjson.ActionDelete, tfjson.ActionCreate}, "↻"},
		{[]tfjson.Action{tfjson.ActionCreate, tfjson.ActionDelete}, "↻"},
		{nil, " "},
	}
	for _, c := range cases {
		if got := actionMarker(c.in); got != c.want {
			t.Errorf("actionMarker(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPlanSummary(t *testing.T) {
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			rc("a", "", "x", "1", change(tfjson.ActionCreate)),
			rc("b", "", "x", "2", change(tfjson.ActionCreate)),
			rc("c", "", "x", "3", change(tfjson.ActionUpdate)),
			rc("d", "", "x", "4", change(tfjson.ActionDelete)),
			rc("e", "", "x", "5", change(tfjson.ActionDelete, tfjson.ActionCreate)), // replace
			rc("f", "", "x", "6", change(tfjson.ActionNoop)),
		},
	}
	want := "Plan: 3 to add, 1 to change, 2 to destroy."
	if got := PlanSummary(plan); got != want {
		t.Errorf("PlanSummary = %q, want %q", got, want)
	}
}

func TestPlanSummary_nilPlan(t *testing.T) {
	if got := PlanSummary(nil); !strings.Contains(got, "no plan") {
		t.Errorf("PlanSummary(nil) = %q", got)
	}
}

func TestBuildPlanTree_groupingAndOrder(t *testing.T) {
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			rc("module.cos_lite.juju_application.grafana", "module.cos_lite", "juju_application", "grafana", change(tfjson.ActionCreate)),
			rc("module.cos_lite.juju_application.alertmanager", "module.cos_lite", "juju_application", "alertmanager", change(tfjson.ActionCreate)),
			rc("module.cos_lite.juju_integration.am_ingress", "module.cos_lite", "juju_integration", "am_ingress", change(tfjson.ActionCreate)),
			rc("juju_model.this", "", "juju_model", "this", change(tfjson.ActionCreate)),
			rc("module.cos_lite.juju_application.noop_thing", "module.cos_lite", "juju_application", "noop_thing", change(tfjson.ActionNoop)),
		},
	}
	root := BuildPlanTree(plan)
	if len(root.Children) != 2 {
		t.Fatalf("expected 2 module groups; got %d", len(root.Children))
	}
	// Modules sorted alphabetically; "" → "(root)".
	if root.Children[0].Label != "(root)" {
		t.Errorf("first module group: %q", root.Children[0].Label)
	}
	cosLite := root.Children[1]
	if cosLite.Label != "module.cos_lite" {
		t.Errorf("second module group: %q", cosLite.Label)
	}
	if len(cosLite.Children) != 2 {
		t.Fatalf("cos_lite type buckets: %d", len(cosLite.Children))
	}
	// Type buckets sorted alphabetically.
	if cosLite.Children[0].Label != "juju_application" {
		t.Errorf("first type bucket: %q", cosLite.Children[0].Label)
	}
	if cosLite.Children[1].Label != "juju_integration" {
		t.Errorf("second type bucket: %q", cosLite.Children[1].Label)
	}
	// Resource leaves sorted alphabetically by Name.
	apps := cosLite.Children[0].Children
	if len(apps) != 2 {
		t.Fatalf("expected 2 non-noop apps, got %d", len(apps))
	}
	if apps[0].Label != "alertmanager" || apps[1].Label != "grafana" {
		t.Errorf("app order: %v / %v", apps[0].Label, apps[1].Label)
	}
}

func TestBuildPlanTree_defaultCollapseState(t *testing.T) {
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			rc("a", "module.x", "t", "r", change(tfjson.ActionCreate)),
		},
	}
	root := BuildPlanTree(plan)
	mod := root.Children[0]
	typ := mod.Children[0]
	if mod.Collapsed {
		t.Error("module should default expanded")
	}
	if !typ.Collapsed {
		t.Error("type bucket should default collapsed")
	}
}

func TestBuildPlanTree_nilPlan(t *testing.T) {
	root := BuildPlanTree(nil)
	if root == nil {
		t.Fatal("nil plan should still produce a sentinel root")
	}
	if len(root.Children) != 0 {
		t.Errorf("nil plan should have no children")
	}
}

func TestFlattenedRows_respectsCollapse(t *testing.T) {
	plan := &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			rc("a", "module.x", "t", "r1", change(tfjson.ActionCreate)),
			rc("b", "module.x", "t", "r2", change(tfjson.ActionCreate)),
		},
	}
	root := BuildPlanTree(plan)
	// Default: module expanded, type collapsed → only the module row and
	// type row are visible (2 rows).
	rows := flattenedRows(root)
	if len(rows) != 2 {
		t.Fatalf("default rows: %d; want module+type only", len(rows))
	}
	// Expand the type bucket and re-flatten.
	root.Children[0].Children[0].Collapsed = false
	rows = flattenedRows(root)
	if len(rows) != 4 {
		t.Fatalf("expanded rows: %d; want 1 module + 1 type + 2 resources", len(rows))
	}
	// Collapse the module → only the module row.
	root.Children[0].Collapsed = true
	rows = flattenedRows(root)
	if len(rows) != 1 {
		t.Fatalf("module-collapsed rows: %d; want only the module", len(rows))
	}
}

func TestResourceLabel_index(t *testing.T) {
	cases := []struct {
		idx  any
		want string
	}{
		{nil, "name"},
		{"key", `name["key"]`},
		{float64(2), "name[2]"},
		{true, "name[true]"},
	}
	for _, c := range cases {
		r := rc("addr", "", "t", "name", change(tfjson.ActionCreate))
		r.Index = c.idx
		if got := resourceLabel(r); got != c.want {
			t.Errorf("resourceLabel(%v) = %q, want %q", c.idx, got, c.want)
		}
	}
}

// Attribute diff --------------------------------------------------------------

func TestAttributeDiff_create(t *testing.T) {
	r := rc("a", "", "juju_application", "alertmanager", &tfjson.Change{
		Actions: []tfjson.Action{tfjson.ActionCreate},
		Before:  nil,
		After: map[string]any{
			"name":  "alertmanager",
			"units": float64(3),
			"trust": true,
		},
	})
	lines := AttributeDiff(r)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	// Sorted by key.
	if lines[0].Key != "name" || lines[0].Marker != "+" || lines[0].After != `"alertmanager"` {
		t.Errorf("name: %+v", lines[0])
	}
	if lines[1].Key != "trust" || lines[1].After != "true" {
		t.Errorf("trust: %+v", lines[1])
	}
	if lines[2].Key != "units" || lines[2].After != "3" {
		t.Errorf("units: %+v", lines[2])
	}
}

func TestAttributeDiff_delete(t *testing.T) {
	r := rc("a", "", "x", "r", &tfjson.Change{
		Actions: []tfjson.Action{tfjson.ActionDelete},
		Before:  map[string]any{"name": "old"},
	})
	lines := AttributeDiff(r)
	if len(lines) != 1 || lines[0].Marker != "-" || lines[0].Before != `"old"` {
		t.Errorf("got %+v", lines)
	}
}

func TestAttributeDiff_update(t *testing.T) {
	r := rc("a", "", "x", "r", &tfjson.Change{
		Actions: []tfjson.Action{tfjson.ActionUpdate},
		Before: map[string]any{
			"name":  "old-name",
			"units": float64(1),
			"gone":  "dropped",
		},
		After: map[string]any{
			"name":  "new-name",
			"units": float64(1), // unchanged
			"added": "fresh",
		},
	})
	lines := AttributeDiff(r)
	if len(lines) != 3 {
		t.Fatalf("expected 3 diff lines (added, gone, name); got %d: %+v", len(lines), lines)
	}
	byKey := map[string]AttributeDiffLine{}
	for _, l := range lines {
		byKey[l.Key] = l
	}
	if l := byKey["added"]; l.Marker != "+" || l.After != `"fresh"` {
		t.Errorf("added: %+v", l)
	}
	if l := byKey["gone"]; l.Marker != "-" || l.Before != `"dropped"` {
		t.Errorf("gone: %+v", l)
	}
	if l := byKey["name"]; l.Marker != "~" || l.Before != `"old-name"` || l.After != `"new-name"` {
		t.Errorf("name: %+v", l)
	}
	if _, ok := byKey["units"]; ok {
		t.Errorf("units unchanged; should not appear in diff")
	}
}

func TestAttributeDiff_sensitiveMasking(t *testing.T) {
	r := rc("a", "", "x", "r", &tfjson.Change{
		Actions: []tfjson.Action{tfjson.ActionCreate},
		After: map[string]any{
			"password": "hunter2",
		},
		AfterSensitive: map[string]any{
			"password": true,
		},
	})
	lines := AttributeDiff(r)
	if len(lines) != 1 {
		t.Fatalf("got %d lines", len(lines))
	}
	if lines[0].After != "<sensitive>" {
		t.Errorf("sensitive value not masked: %q", lines[0].After)
	}
	if strings.Contains(lines[0].String(), "hunter2") {
		t.Errorf("rendered line leaked secret: %q", lines[0].String())
	}
}

func TestAttributeDiff_replaceLikeUpdate(t *testing.T) {
	r := rc("a", "", "x", "r", &tfjson.Change{
		Actions: []tfjson.Action{tfjson.ActionDelete, tfjson.ActionCreate},
		Before:  map[string]any{"id": "old"},
		After:   map[string]any{"id": "new"},
	})
	lines := AttributeDiff(r)
	if len(lines) != 1 || lines[0].Marker != "~" {
		t.Errorf("replace should produce update-style diff: %+v", lines)
	}
}

func TestAttributeDiff_noopAndRead_empty(t *testing.T) {
	for _, action := range []tfjson.Action{tfjson.ActionNoop, tfjson.ActionRead} {
		r := rc("a", "", "x", "r", &tfjson.Change{
			Actions: []tfjson.Action{action},
			After:   map[string]any{"x": "y"},
		})
		if got := AttributeDiff(r); got != nil {
			t.Errorf("action %v should produce empty diff; got %+v", action, got)
		}
	}
}

func TestRenderValue(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, "null"},
		{"hello", `"hello"`},
		{true, "true"},
		{float64(3), "3"},
		{float64(3.14), "3.14"},
		{[]any{"a", float64(1)}, `["a", 1]`},
		{map[string]any{"k": "v"}, `{k="v"}`},
	}
	for _, c := range cases {
		if got := renderValue(c.in, false); got != c.want {
			t.Errorf("renderValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := renderValue("secret", true); got != "<sensitive>" {
		t.Errorf("sensitive: %q", got)
	}
}

func TestAttributeDiffLine_String(t *testing.T) {
	cases := []struct {
		in   AttributeDiffLine
		want string
	}{
		{AttributeDiffLine{Marker: "+", Key: "x", After: `"a"`}, `+ x = "a"`},
		{AttributeDiffLine{Marker: "-", Key: "x", Before: `"a"`}, `- x = "a"`},
		{AttributeDiffLine{Marker: "~", Key: "x", Before: `"a"`, After: `"b"`}, `~ x = "a" → "b"`},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
