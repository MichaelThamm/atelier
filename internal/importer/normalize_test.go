package importer

import (
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

func rcNullEmpty(addr string, before, after, unknown map[string]interface{}) *tfjson.ResourceChange {
	return &tfjson.ResourceChange{
		Address: addr,
		Type:    "juju_application",
		Change: &tfjson.Change{
			Actions:      tfjson.Actions{tfjson.ActionUpdate},
			Before:       before,
			After:        after,
			AfterUnknown: unknown,
		},
	}
}

// The reported regression: state holds {} because the old pooled-defaults step
// wrote it, while the module call never passes the argument so config wants null.
// The fix must write null.
func TestEmptyNullFixes_EmptyStateNullConfig(t *testing.T) {
	const addr = "module.cos_lite.module.ssc[0].juju_application.self-signed-certificates"
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		rcNullEmpty(addr,
			map[string]interface{}{"resources": map[string]interface{}{}, "storage_directives": map[string]interface{}{}},
			map[string]interface{}{"resources": nil, "storage_directives": nil},
			nil),
	}}
	got := EmptyNullFixes(plan, map[string]bool{addr: true})
	fixes, ok := got[addr]
	if !ok {
		t.Fatalf("no fixes produced: %v", got)
	}
	for _, k := range []string{"resources", "storage_directives"} {
		v, present := fixes[k]
		if !present {
			t.Errorf("%s not fixed", k)
		}
		if v != nil {
			t.Errorf("%s = %v, want nil", k, v)
		}
	}
}

// The other direction: provider stored null, config computes {}.
func TestEmptyNullFixes_NullStateEmptyConfig(t *testing.T) {
	const addr = "juju_application.a"
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		rcNullEmpty(addr,
			map[string]interface{}{"config": nil},
			map[string]interface{}{"config": map[string]interface{}{}},
			nil),
	}}
	got := EmptyNullFixes(plan, map[string]bool{addr: true})
	v, present := got[addr]["config"]
	if !present {
		t.Fatalf("config not fixed: %v", got)
	}
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 0 {
		t.Errorf("config = %#v, want empty map", v)
	}
}

// The safety property: real drift must survive untouched. A charm revision
// moving 198 -> 199 is a genuine upgrade, not representational noise.
func TestEmptyNullFixes_LeavesRealDriftAlone(t *testing.T) {
	const addr = "juju_application.grafana"
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		rcNullEmpty(addr,
			map[string]interface{}{
				"charm":       []interface{}{map[string]interface{}{"revision": float64(198)}},
				"constraints": "arch=amd64",
				"units":       float64(1),
			},
			map[string]interface{}{
				"charm":       []interface{}{map[string]interface{}{"revision": float64(199)}},
				"constraints": "",
				"units":       float64(2),
			},
			nil),
	}}
	if got := EmptyNullFixes(plan, map[string]bool{addr: true}); got != nil {
		t.Errorf("real drift must not be normalized, got %v", got)
	}
}

// A non-empty collection changing is real drift too.
func TestEmptyNullFixes_LeavesNonEmptyCollectionsAlone(t *testing.T) {
	const addr = "juju_application.a"
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		rcNullEmpty(addr,
			map[string]interface{}{"config": map[string]interface{}{"k": "v"}},
			map[string]interface{}{"config": nil},
			nil),
	}}
	if got := EmptyNullFixes(plan, map[string]bool{addr: true}); got != nil {
		t.Errorf("dropping a populated map is real drift, got %v", got)
	}
}

// Computed attributes have no concrete planned value to reconcile against.
func TestEmptyNullFixes_SkipsUnknownAfterValues(t *testing.T) {
	const addr = "juju_application.a"
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		rcNullEmpty(addr,
			map[string]interface{}{"storage": []interface{}{}},
			map[string]interface{}{"storage": nil},
			map[string]interface{}{"storage": true}),
	}}
	if got := EmptyNullFixes(plan, map[string]bool{addr: true}); got != nil {
		t.Errorf("unknown planned values must be skipped, got %v", got)
	}
}

// Only resources this run imported are eligible.
func TestEmptyNullFixes_IgnoresUnimportedAddresses(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		rcNullEmpty("juju_application.other",
			map[string]interface{}{"resources": map[string]interface{}{}},
			map[string]interface{}{"resources": nil},
			nil),
	}}
	if got := EmptyNullFixes(plan, map[string]bool{"juju_application.mine": true}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestEmptyNullFixes_NilPlan(t *testing.T) {
	if got := EmptyNullFixes(nil, map[string]bool{"a": true}); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

// Empty strings and zero numbers are ordinary values, not empty collections.
func TestIsEmptyCollection(t *testing.T) {
	cases := []struct {
		v    interface{}
		want bool
	}{
		{map[string]interface{}{}, true},
		{[]interface{}{}, true},
		{map[string]interface{}{"a": 1}, false},
		{[]interface{}{1}, false},
		{"", false},
		{float64(0), false},
		{nil, false},
		{false, false},
	}
	for _, c := range cases {
		if got := isEmptyCollection(c.v); got != c.want {
			t.Errorf("isEmptyCollection(%#v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestNullEmptyNormalization_Name(t *testing.T) {
	if got := (&NullEmptyNormalization{}).Name(); got != "Normalize null/empty attributes" {
		t.Errorf("Name() = %q", got)
	}
}

// No plan available must be survivable: the import already succeeded.
func TestNullEmptyNormalization_NilPlanFunc(t *testing.T) {
	step := &NullEmptyNormalization{}
	if err := step.Run(nil, PostImportContext{Dir: t.TempDir(), Imported: []ImportResult{{Address: "a"}}}); err != nil {
		t.Errorf("nil Plan func should be tolerated, got %v", err)
	}
}
