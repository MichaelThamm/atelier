package main

import "errors"

// Exit codes. `atelier apply` runs Terraform, so it can fail in two ways that
// mean different things to whoever is running it: Atelier declined to write the
// wrapper, or the wrapper is fine and the deployment failed. Both used to exit 1,
// so a CI job could not tell "fix your wrapper" from "look at the infrastructure"
// without scraping stderr.
//
// Only `apply` uses these. Every other command exits 0 or 1 and carries its
// reason in the --json payload (ADR-0048) or on stderr.
const (
	// exitOK is success.
	exitOK = 0
	// exitAtelier is a failure Atelier owns: bad usage, a clone that failed, a
	// refusal (a duplicate module, an ambiguous wrapper, an --as naming another
	// module), the required-input gate, a preflight refusal. Nothing was
	// deployed, and the wrapper on disk is either unchanged or freshly written.
	exitAtelier = 1
	// exitTerraform is Terraform having run and reported failure: `terraform
	// init` or `terraform apply` exited non-zero. The wrapper on disk is current,
	// and infrastructure may be partly applied, so this is not a safe blind
	// re-run without reading the plan first.
	exitTerraform = 2
)

// exitError carries the code a command should exit with, so a failure deep in a
// call chain can classify itself without every caller re-deciding.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// terraformFailure marks an error as Terraform's rather than Atelier's. It wraps
// the underlying error rather than replacing it, so the message still names the
// command and its exit status — which is what a human reads first.
func terraformFailure(err error) error {
	if err == nil {
		return nil
	}
	return &exitError{code: exitTerraform, err: err}
}

// exitCodeFor is the process exit status for err. Anything unclassified is
// Atelier's: if Atelier cannot tell, the failure is its own.
func exitCodeFor(err error) int {
	if err == nil {
		return exitOK
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return exitAtelier
}
