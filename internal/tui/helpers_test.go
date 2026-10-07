package tui

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// A required variable counts as unset unless it holds a concrete value or a
// wired expression. "Concrete" excludes an explicit null, which is what a
// freshly bootstrapped wrapper writes as a placeholder: `main.tf` gets
// `model_uuid = null` (wrapper.bootstrapMain) and that reads back as a
// present-but-null value. `atelier apply` refuses on the same condition via
// unsetRequiredVars; the [!] marker and the ref-switch warning must agree.
func TestRequiredUnsetCount(t *testing.T) {
	string_ := mustParseType(t, "string")
	vars := []tfvars.Variable{
		{Name: "model_uuid", Type: string_}, // required, no default
		{Name: "channel", Type: string_, HasDefault: true, Default: cty.StringVal("dev")},
	}

	tests := []struct {
		name   string
		values map[string]cty.Value
		attrs  []wrapper.RawAttr
		want   int
	}{
		{
			name:   "unset required variable counts",
			values: map[string]cty.Value{},
			want:   1,
		},
		{
			name:   "explicit null placeholder counts as unset",
			values: map[string]cty.Value{"model_uuid": cty.NullVal(cty.String)},
			want:   1,
		},
		{
			name:   "NilVal counts as unset",
			values: map[string]cty.Value{"model_uuid": cty.NilVal},
			want:   1,
		},
		{
			name:   "concrete value does not count",
			values: map[string]cty.Value{"model_uuid": cty.StringVal("abc")},
			want:   0,
		},
		{
			name:   "optional never counts, even null",
			values: map[string]cty.Value{"channel": cty.NullVal(cty.String)},
			want:   1, // model_uuid only; channel has a default
		},
		{
			name:   "wired expression does not count",
			values: map[string]cty.Value{},
			attrs:  []wrapper.RawAttr{{Name: "model_uuid", RawExpr: []byte("module.loki.endpoint")}},
			want:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &wrapper.State{
				Vars:         vars,
				Values:       tc.values,
				UnknownAttrs: tc.attrs,
			}
			if got := requiredUnsetCount(st); got != tc.want {
				t.Errorf("requiredUnsetCount = %d; want %d", got, tc.want)
			}
		})
	}
}

func TestRequiredUnsetCount_nilState(t *testing.T) {
	if got := requiredUnsetCount(nil); got != 0 {
		t.Errorf("requiredUnsetCount(nil) = %d; want 0", got)
	}
}
