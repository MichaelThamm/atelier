package integrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/tftypes"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// wrapperWith writes a main.tf composing producer and consumer local modules.
func wrapperWith(t *testing.T, producerOutput, consumerInput string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.tf"), `module "cos_lite" {
  source = "./producer"
}
module "charmed_spark" {
  source = "./consumer"
}
`)
	write(t, filepath.Join(dir, "producer", "outputs.tf"), producerOutput)
	write(t, filepath.Join(dir, "consumer", "variables.tf"), consumerInput)
	return dir
}

func TestPropose_wholeObjectSubsetMatches(t *testing.T) {
	dir := wrapperWith(t,
		`output "cos_offers" {
  type = object({ grafana_dashboard = string, loki_push_api = string, prometheus_remote_write = string })
  value = { grafana_dashboard = "a", loki_push_api = "b", prometheus_remote_write = "c" }
}
`,
		`variable "cos_offers" {
  type = object({ grafana_dashboard = string, prometheus_remote_write = string })
}
`)
	got, err := Propose(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("proposals = %+v, want 1", got)
	}
	p := got[0]
	if p.Consumer != "charmed_spark" || p.Input != "cos_offers" || p.Producer != "cos_lite" || p.Output != "cos_offers" {
		t.Errorf("proposal = %+v", p)
	}
	if strings.Join(p.Interfaces, ",") != "grafana_dashboard,prometheus_remote_write" {
		t.Errorf("interfaces = %v, want the consumer's two keys", p.Interfaces)
	}
}

func TestPropose_noMatch(t *testing.T) {
	cases := map[string]struct {
		output string
		input  string
	}{
		"different names": {
			`output "cos_offers" { type = object({a = string}) value = {a = "x"} }`,
			`variable "observability" { type = object({a = string}) }`,
		},
		"undeclared producer type": {
			`output "cos_offers" { value = "x" }`,
			`variable "cos_offers" { type = object({a = string}) }`,
		},
		"consumer requires a field the producer lacks": {
			`output "cos_offers" { type = object({a = string}) value = {a = "x"} }`,
			`variable "cos_offers" { type = object({a = string, b = string}) }`,
		},
		"incompatible field type": {
			`output "cos_offers" { type = object({a = number}) value = {a = 1} }`,
			`variable "cos_offers" { type = object({a = string}) }`,
		},
		"scalar same-name is not an interface": {
			`output "model_uuid" { type = string value = "x" }`,
			`variable "model_uuid" { type = string }`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Propose(wrapperWith(t, tc.output, tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Errorf("proposals = %+v, want none", got)
			}
		})
	}
}

func TestPropose_reportsAnExistingWholeObjectWire(t *testing.T) {
	dir := wrapperWith(t,
		`output "cos_offers" {
  type  = object({ a = string })
  value = { a = "x" }
}
`,
		`variable "cos_offers" {
  type = object({ a = string })
}
`,
	)
	write(t, filepath.Join(dir, "main.tf"), `module "cos_lite" {
  source = "./producer"
}
module "charmed_spark" {
  source     = "./consumer"
  cos_offers = module.cos_lite.cos_offers
}
`)
	got, err := Propose(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("proposals = %+v, want one existing integration", got)
	}
	if !got[0].Existing || got[0].Selected != nil {
		t.Errorf("proposal = %+v, want Existing with nil (whole-object) selection", got[0])
	}
}

func TestPropose_infersAnExistingSubset(t *testing.T) {
	dir := wrapperWith(t,
		`output "cos_offers" {
  type  = object({ a = string, b = string })
  value = { a = "1", b = "2" }
}
`,
		`variable "cos_offers" {
  type = map(string)
}
`,
	)
	write(t, filepath.Join(dir, "main.tf"), `module "cos_lite" {
  source = "./producer"
}
module "charmed_spark" {
  source     = "./consumer"
  cos_offers = { a = module.cos_lite.cos_offers.a }
}
`)
	got, err := Propose(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("proposals = %+v, want one existing integration", got)
	}
	p := got[0]
	if !p.Existing || strings.Join(p.Selected, ",") != "a" {
		t.Errorf("proposal = %+v, want Existing selecting just \"a\"", p)
	}
}

func TestPropose_mapConsumerIsSubsettable(t *testing.T) {
	dir := wrapperWith(t,
		`output "cos_offers" {
  type  = object({ a = string, b = string })
  value = { a = "1", b = "2" }
}
`,
		`variable "cos_offers" {
  type = map(string)
}
`)
	got, err := Propose(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("proposals = %+v, want 1", got)
	}
	p := got[0]
	if !p.Subsettable {
		t.Error("a map(string) consumer should be subsettable")
	}
	if strings.Join(p.Interfaces, ",") != "a,b" {
		t.Errorf("interfaces = %v, want the producer's keys", p.Interfaces)
	}
}

func TestCompatible(t *testing.T) {
	obj := func(attrs map[string]tftypes.Kind) *tftypes.Type {
		ty := &tftypes.Type{Kind: tftypes.KindObject, Attributes: map[string]*tftypes.ObjectAttr{}}
		for name, kind := range attrs {
			ty.AttrOrder = append(ty.AttrOrder, name)
			ty.Attributes[name] = &tftypes.ObjectAttr{Type: &tftypes.Type{Kind: kind}}
		}
		return ty
	}
	if !Compatible(obj(map[string]tftypes.Kind{"a": tftypes.KindString}), obj(map[string]tftypes.Kind{"a": tftypes.KindString, "b": tftypes.KindString})) {
		t.Error("subset with equal types should be compatible")
	}
	if Compatible(obj(map[string]tftypes.Kind{"a": tftypes.KindString}), obj(map[string]tftypes.Kind{"b": tftypes.KindString})) {
		t.Error("missing attribute should be incompatible")
	}
	if Compatible(&tftypes.Type{Kind: tftypes.KindAny}, obj(map[string]tftypes.Kind{"a": tftypes.KindString})) {
		t.Error("undeclared type should be incompatible")
	}
	mapStr := &tftypes.Type{Kind: tftypes.KindMap, Element: &tftypes.Type{Kind: tftypes.KindString}}
	if !isInterfaceInput(mapStr) {
		t.Error("map(string) should be an interface input")
	}
	if !Compatible(mapStr, obj(map[string]tftypes.Kind{"a": tftypes.KindString})) {
		t.Error("map(string) should accept an all-string object")
	}
}
