package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// listOfObjectsLikeVar mirrors haproxy's
// `protected_hostnames_configuration`, which is a required list(object(...))
// on a gallery entry.
func listOfObjectsLikeVar(t *testing.T) tfvars.Variable {
	t.Helper()
	return tfvars.Variable{
		Name: "protected_hostnames_configuration",
		Type: mustParseType(t, `list(object({
    hostname         = string
    oauth_integrator = optional(bool, false)
  }))`),
	}
}

// A list or set whose elements Atelier cannot render must not report a value.
// The old listEditor stringified each element with cty's GoString and rebuilt
// the collection as a list of strings, so a list(object) came back as
// list(string). Because the model pushes the live editor's value into state on
// every keystroke, one ignored keypress rewrote a hand-authored
// `protected_hostnames_configuration` as a list of Go source fragments and
// Terraform rejected the wrapper on the next plan.
func TestCompositeListIsNotEditableAndKeepsItsValue(t *testing.T) {
	dir := t.TempDir()
	before := "module \"haproxy\" {\n" +
		"  source = \"git::example/haproxy//product?ref=main\"\n" +
		"  protected_hostnames_configuration = [\n" +
		"    {\n" +
		"      hostname = \"admin.example.com\"\n" +
		"    },\n" +
		"  ]\n" +
		"  units = 3\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := []tfvars.Variable{
		listOfObjectsLikeVar(t),
		{Name: "units", Type: mustParseType(t, "number"), HasDefault: true},
	}
	parsed, err := wrapper.ReadMainForBlock(dir, "haproxy", vars)
	if err != nil {
		t.Fatal(err)
	}
	st := &wrapper.State{
		Dir: dir, ModuleBlockName: "haproxy", Source: parsed.Source,
		Vars: vars, Values: parsed.Values, UnknownAttrs: parsed.UnknownAttrs,
	}

	m := New(st, "haproxy")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = feed(m, key("tab")) // focus the editor

	// A read-only editor does not implement EditorWithValue, which is what
	// stops update.go pushing a value into state on every tick.
	if _, pushes := m.editor.(EditorWithValue); pushes {
		t.Errorf("editor = %T; a collection Atelier cannot render must not report a value", m.editor)
	}

	// Any keystroke, including one the editor ignores, must leave the
	// wrapper's value alone.
	for _, k := range []string{"z", "a", "d", "enter", "ctrl+r"} {
		m = feed(m, key(k))
		if _, pushes := m.editor.(EditorWithValue); pushes {
			t.Fatalf("after %q the editor became %T and would push a value", k, m.editor)
		}
	}
	if got := st.Values["protected_hostnames_configuration"]; !got.Type().IsTupleType() &&
		!got.Type().IsListType() {
		t.Errorf("in-memory value changed type: %s", got.Type().GoString())
	}
	if err := m.SaveIfDirty(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "cty.ObjectVal") {
		t.Errorf("the value was rewritten as Go source:\n%s", out)
	}
	got := st.Values["protected_hostnames_configuration"]
	if want := parsed.Values["protected_hostnames_configuration"]; !got.RawEquals(want) {
		t.Errorf("value = %s, want %s", got.GoString(), want.GoString())
	}
}

// The same holds for a set of objects: it took the same code path.
func TestCompositeSetIsNotEditable(t *testing.T) {
	v := tfvars.Variable{
		Name: "endpoint_bindings",
		Type: mustParseType(t, `set(object({
    endpoint = string
    port     = optional(number, 80)
  }))`),
	}
	ed := newEditor(&v, cty.SetValEmpty(cty.Object(map[string]cty.Type{"endpoint": cty.String})))
	if _, pushes := ed.(EditorWithValue); pushes {
		t.Errorf("set(object) editor = %T; must not report a value", ed)
	}
}

// A list of scalars is not editable yet either — the old widget had no caret,
// so there was no way to put a value into an entry. readOnlyEditor reports
// nothing rather than offering `a`/`d` over an uneditable buffer.
func TestScalarListIsNotEditableYet(t *testing.T) {
	v := tfvars.Variable{
		Name: "constraints",
		Type: mustParseType(t, "list(string)"),
	}
	ed := newEditor(&v, cty.ListVal([]cty.Value{cty.StringVal("arch=amd64")}))
	if _, pushes := ed.(EditorWithValue); pushes {
		t.Errorf("list(string) editor = %T; must not report a value", ed)
	}
	view := stripANSI(ed.View())
	if !strings.Contains(view, "main.tf") {
		t.Errorf("a read-only collection should say where to edit it instead; got:\n%s", view)
	}
}

// The read-only view names the collection, how many entries it holds, and
// where to change it — so a user is never left guessing what `[a] add` used to
// do.
func TestReadOnlyCollectionSaysWhereToEdit(t *testing.T) {
	v := tfvars.Variable{Name: "constraints", Type: mustParseType(t, "list(string)")}
	ed := newEditor(&v, cty.ListVal([]cty.Value{cty.StringVal("arch=amd64")}))
	ro, ok := ed.(*readOnlyEditor)
	if !ok {
		t.Fatalf("editor = %T, want *readOnlyEditor", ed)
	}
	for _, want := range []string{"List", "1 entries", "main.tf", "--var constraints="} {
		if !strings.Contains(ro.text, want) {
			t.Errorf("read-only text %q is missing %q", ro.text, want)
		}
	}
	// A set says so.
	sv := tfvars.Variable{Name: "endpoint_bindings", Type: mustParseType(t, "set(string)")}
	sro := newEditor(&sv, cty.SetValEmpty(cty.String)).(*readOnlyEditor)
	if !strings.Contains(sro.text, "Set") {
		t.Errorf("a set should be tagged Set; got %q", sro.text)
	}
}

// Ctrl+R on a read-only editor must not delete the variable. The reset drops
// the entry from state, so the sparse-write rule would then prune it from
// main.tf — losing a hand-written collection behind a keystroke that reads as
// "put it back to the default".
func TestResetOnReadOnlyCollectionDoesNotDelete(t *testing.T) {
	state := sampleState(t)
	state.Vars = append(state.Vars, listOfObjectsLikeVar(t))
	state.Values["protected_hostnames_configuration"] = cty.ListVal([]cty.Value{
		cty.ObjectVal(map[string]cty.Value{"hostname": cty.StringVal("admin.example.com")}),
	})
	m := New(state, "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	for m.SelectedVariable().Name != "protected_hostnames_configuration" {
		m = feed(m, key("down"))
	}
	before := state.Values["protected_hostnames_configuration"]
	m = feed(m, key("tab"))
	m = feed(m, key("ctrl+r"))
	after, still := state.Values["protected_hostnames_configuration"]
	if !still {
		t.Fatalf("Ctrl+R deleted the collection; status was %q", m.status)
	}
	if !after.RawEquals(before) {
		t.Errorf("value changed: %s → %s", before.GoString(), after.GoString())
	}
}
