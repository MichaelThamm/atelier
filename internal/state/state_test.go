package state

import (
	"os"
	"path/filepath"
	"testing"
)

// --- buildAddress ---

func TestBuildAddress_NoModule(t *testing.T) {
	got := buildAddress("", "juju_application", "grafana", nil)
	if got != "juju_application.grafana" {
		t.Errorf("got %q, want %q", got, "juju_application.grafana")
	}
}

func TestBuildAddress_WithModule(t *testing.T) {
	got := buildAddress("module.cos_lite", "juju_application", "grafana", nil)
	if got != "module.cos_lite.juju_application.grafana" {
		t.Errorf("got %q", got)
	}
}

func TestBuildAddress_StringIndex(t *testing.T) {
	got := buildAddress("", "juju_application", "grafana", "myapp")
	want := `juju_application.grafana["myapp"]`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildAddress_Float64Index(t *testing.T) {
	got := buildAddress("", "juju_application", "grafana", float64(3))
	want := "juju_application.grafana[3]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- formatValue ---

func TestFormatValue_Nil(t *testing.T) {
	if got := formatValue(nil); got != "null" {
		t.Errorf("got %q, want %q", got, "null")
	}
}

func TestFormatValue_String(t *testing.T) {
	got := formatValue("hello")
	if got != `"hello"` {
		t.Errorf("got %q", got)
	}
}

func TestFormatValue_Float64Integer(t *testing.T) {
	got := formatValue(float64(42))
	if got != "42" {
		t.Errorf("got %q, want %q", got, "42")
	}
}

func TestFormatValue_Float64Fractional(t *testing.T) {
	got := formatValue(float64(3.14))
	if got != "3.14" {
		t.Errorf("got %q, want %q", got, "3.14")
	}
}

func TestFormatValue_BoolTrue(t *testing.T) {
	if got := formatValue(true); got != "true" {
		t.Errorf("got %q", got)
	}
}

func TestFormatValue_BoolFalse(t *testing.T) {
	if got := formatValue(false); got != "false" {
		t.Errorf("got %q", got)
	}
}

func TestFormatValue_EmptySlice(t *testing.T) {
	got := formatValue([]interface{}{})
	if got != "[]" {
		t.Errorf("got %q, want %q", got, "[]")
	}
}

func TestFormatValue_NonEmptySlice(t *testing.T) {
	got := formatValue([]interface{}{"a", "b"})
	if got != `["a","b"]` {
		t.Errorf("got %q", got)
	}
}

func TestFormatValue_EmptyMap(t *testing.T) {
	got := formatValue(map[string]interface{}{})
	if got != "{}" {
		t.Errorf("got %q, want %q", got, "{}")
	}
}

func TestFormatValue_NonEmptyMap(t *testing.T) {
	got := formatValue(map[string]interface{}{"k": "v"})
	if got != `{"k":"v"}` {
		t.Errorf("got %q", got)
	}
}

// --- SummaryLine ---

func TestSummaryLine_NilState(t *testing.T) {
	var s *State
	if got := s.SummaryLine(); got != "State: empty" {
		t.Errorf("got %q", got)
	}
}

func TestSummaryLine_EmptyState(t *testing.T) {
	s := &State{}
	if got := s.SummaryLine(); got != "State: empty" {
		t.Errorf("got %q", got)
	}
}

func TestSummaryLine_SingleModule(t *testing.T) {
	s := &State{
		Summary: Summary{
			Total:    5,
			ByModule: map[string]int{"": 5},
		},
	}
	got := s.SummaryLine()
	want := "State: 5 resource(s)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSummaryLine_MultipleModules(t *testing.T) {
	s := &State{
		Summary: Summary{
			Total:    10,
			ByModule: map[string]int{"": 3, "module.a": 4, "module.b": 3},
		},
	}
	got := s.SummaryLine()
	want := "State: 10 resource(s) across 3 modules"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// --- AttributeLines ---

func TestAttributeLines_Empty(t *testing.T) {
	r := &Resource{Attributes: map[string]interface{}{}}
	if got := r.AttributeLines(); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestAttributeLines_Sorted(t *testing.T) {
	r := &Resource{
		Attributes: map[string]interface{}{
			"zebra": "z",
			"alpha": "a",
			"mid":   float64(5),
		},
	}
	got := r.AttributeLines()
	want := []string{`alpha = "a"`, "mid = 5", `zebra = "z"`}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d: got %q, want %q", i, got[i], w)
		}
	}
}

// --- Parse ---

func TestParse_EmptyJSON(t *testing.T) {
	got, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse([]byte(`not json`))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParse_SingleResource(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "grafana",
			"provider": "provider[\"registry.terraform.io/juju/juju\"]",
			"instances": [{
				"index_key": null,
				"attributes": {"name": "grafana", "charm": "grafana-k8s"}
			}]
		}]
	}`)
	st, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("expected non-nil state")
	}
	if st.Summary.Total != 1 {
		t.Errorf("got %d resources, want 1", st.Summary.Total)
	}
	if len(st.Resources) != 1 {
		t.Fatalf("got %d resources, want 1", len(st.Resources))
	}
	r := st.Resources[0]
	if r.Address != "juju_application.grafana" {
		t.Errorf("address: got %q", r.Address)
	}
	if r.Type != "juju_application" {
		t.Errorf("type: got %q", r.Type)
	}
	if r.Attributes["name"] != "grafana" {
		t.Errorf("attribute name: got %v", r.Attributes["name"])
	}
}

func TestParse_IndexedResource(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "juju_application",
			"name": "app",
			"instances": [{
				"index_key": "primary",
				"attributes": {}
			}]
		}]
	}`)
	st, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Resources[0].Address != `juju_application.app["primary"]` {
		t.Errorf("address: got %q", st.Resources[0].Address)
	}
}

func TestParse_NumericIndex(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [{
			"module": "",
			"mode": "managed",
			"type": "aws_instance",
			"name": "web",
			"instances": [{
				"index_key": 0,
				"attributes": {}
			}]
		}]
	}`)
	st, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Resources[0].Address != "aws_instance.web[0]" {
		t.Errorf("address: got %q", st.Resources[0].Address)
	}
}

func TestParse_MultiModule(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{
				"module": "",
				"mode": "managed",
				"type": "juju_model",
				"name": "default",
				"instances": [{"attributes": {}}]
			},
			{
				"module": "module.cos_lite",
				"mode": "managed",
				"type": "juju_application",
				"name": "grafana",
				"instances": [{"attributes": {}}]
			},
			{
				"module": "module.cos_lite",
				"mode": "managed",
				"type": "juju_application",
				"name": "prometheus",
				"instances": [{"attributes": {}}]
			}
		]
	}`)
	st, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Summary.Total != 3 {
		t.Errorf("total: got %d, want 3", st.Summary.Total)
	}
	if len(st.Summary.ByModule) != 2 {
		t.Errorf("modules: got %d, want 2", len(st.Summary.ByModule))
	}
	if st.Summary.ByModule[""] != 1 {
		t.Errorf("root count: got %d, want 1", st.Summary.ByModule[""])
	}
	if st.Summary.ByModule["module.cos_lite"] != 2 {
		t.Errorf("cos_lite count: got %d, want 2", st.Summary.ByModule["module.cos_lite"])
	}
	// Verify sorted order: root first, then module.cos_lite resources sorted by address.
	if st.Resources[0].Module != "" {
		t.Errorf("first resource module: got %q, want root", st.Resources[0].Module)
	}
	if st.Resources[1].Address != "module.cos_lite.juju_application.grafana" {
		t.Errorf("second resource: got %q", st.Resources[1].Address)
	}
}

func TestParse_SortedByAddress(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{"module": "", "mode": "managed", "type": "b", "name": "z", "instances": [{"attributes": {}}]},
			{"module": "", "mode": "managed", "type": "a", "name": "z", "instances": [{"attributes": {}}]}
		]
	}`)
	st, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if st.Resources[0].Address != "a.z" {
		t.Errorf("first: got %q, want a.z", st.Resources[0].Address)
	}
	if st.Resources[1].Address != "b.z" {
		t.Errorf("second: got %q, want b.z", st.Resources[1].Address)
	}
}

// --- Read ---

func TestRead_NoStateFile(t *testing.T) {
	dir := t.TempDir()
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestRead_ValidState(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"version": 4, "resources": []}`)
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), data, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected non-nil state")
	}
	if got.Summary.Total != 0 {
		t.Errorf("got %d resources, want 0", got.Summary.Total)
	}
}

func TestRead_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte("{bad"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := Read(dir)
	if err == nil {
		t.Error("expected error for malformed state")
	}
}
