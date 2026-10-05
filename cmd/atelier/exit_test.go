package main

import (
	"errors"
	"fmt"
	"testing"
)

// exitCodeFor is the whole contract a CI job depends on: which side failed.
// The wrapper-error cases are the refusals ADR-0050 introduced, which all arrive
// as plain errors from the compose decision.
func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "success",
			err:  nil,
			want: exitOK,
		},
		{
			name: "a refusal is Atelier's",
			err:  ambiguousModuleError(nil, composeRequest{}),
			want: exitAtelier,
		},
		{
			name: "an unclassified error is Atelier's",
			err:  errors.New("module requires a value for model_uuid"),
			want: exitAtelier,
		},
		{
			name: "a terraform failure is Terraform's",
			err:  terraformFailure(errors.New("exit status 1")),
			want: exitTerraform,
		},
		{
			name: "the classification survives wrapping",
			err:  fmt.Errorf("apply: %w", terraformFailure(errors.New("exit status 1"))),
			want: exitTerraform,
		},
		{
			// The message a human reads must not change: it still names the
			// command and its exit status.
			name: "the message is preserved",
			err:  terraformFailure(fmt.Errorf("terraform init: %w", errors.New("boom"))),
			want: exitTerraform,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := exitCodeFor(c.err); got != c.want {
				t.Errorf("exitCodeFor(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// The message must survive classification verbatim, since it is what a human
// reads first and what the existing docs describe.
func TestTerraformFailure_preservesTheMessage(t *testing.T) {
	inner := errors.New("exit status 1")
	err := terraformFailure(fmt.Errorf("terraform apply: %w", inner))
	if got, want := err.Error(), "terraform apply: exit status 1"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, inner) {
		t.Error("the underlying error must stay unwrappable")
	}
	if terraformFailure(nil) != nil {
		t.Error("classifying nil must stay nil, or callers would wrap a success")
	}
}
