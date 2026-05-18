// Package tfexec wraps hashicorp/terraform-exec to give Atelier a narrow,
// testable surface over the Terraform binary: locate it, query its version,
// run init / validate / providers schema / plan / apply, and parse the JSON
// output.
package tfexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"

	hcversion "github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
)

// MinVersion is the lowest terraform version Atelier supports (SPEC §5.2 ¶5).
const MinVersion = "1.5.0"

// Locate returns the path to the terraform (or tofu) binary on $PATH, or an
// actionable error message if it isn't installed.
func Locate() (string, error) {
	for _, name := range []string{"terraform", "tofu"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", errors.New("could not find terraform or tofu on $PATH; install Terraform >= " + MinVersion)
}

// Terraform wraps a *tfexec.Terraform and re-exports the small set of
// operations Atelier needs. Callers should treat the returned type as
// opaque: it exists primarily so tests can substitute a stub via the
// Operations interface.
type Terraform struct {
	tf *tfexec.Terraform
}

// New returns a Terraform pinned to the wrapper directory `workdir`. If
// `binPath` is empty, Locate is used to find a terraform/tofu binary.
func New(workdir, binPath string) (*Terraform, error) {
	if binPath == "" {
		var err error
		binPath, err = Locate()
		if err != nil {
			return nil, err
		}
	}
	tf, err := tfexec.NewTerraform(workdir, binPath)
	if err != nil {
		return nil, fmt.Errorf("init terraform-exec: %w", err)
	}
	return &Terraform{tf: tf}, nil
}

// Inner exposes the underlying tfexec.Terraform for callers that need
// advanced configuration (e.g. logging).
func (t *Terraform) Inner() *tfexec.Terraform { return t.tf }

// Version returns the resolved terraform version string.
func (t *Terraform) Version(ctx context.Context) (string, error) {
	v, _, err := t.tf.Version(ctx, true)
	if err != nil {
		return "", fmt.Errorf("query terraform version: %w", err)
	}
	return v.String(), nil
}

// CheckVersion ensures the binary is at least MinVersion.
func (t *Terraform) CheckVersion(ctx context.Context) (string, error) {
	v, _, err := t.tf.Version(ctx, true)
	if err != nil {
		return "", fmt.Errorf("query terraform version: %w", err)
	}
	mv, _ := hcversion.NewVersion(MinVersion)
	if v.LessThan(mv) {
		return v.String(), fmt.Errorf("terraform version %s is older than the required %s", v, MinVersion)
	}
	return v.String(), nil
}

// Init runs `terraform init`.
func (t *Terraform) Init(ctx context.Context) error {
	return t.tf.Init(ctx, tfexec.Upgrade(false))
}

// InitUpgrade runs `terraform init -upgrade` to update module sources and
// providers to the latest allowed versions. Used after a ref switch so
// Terraform fetches the new module revision.
func (t *Terraform) InitUpgrade(ctx context.Context) error {
	return t.tf.Init(ctx, tfexec.Upgrade(true))
}

// Validate runs `terraform validate -json`.
func (t *Terraform) Validate(ctx context.Context) (*tfjson.ValidateOutput, error) {
	return t.tf.Validate(ctx)
}

// ProvidersSchema runs `terraform providers schema -json`.
func (t *Terraform) ProvidersSchema(ctx context.Context) (*tfjson.ProviderSchemas, error) {
	return t.tf.ProvidersSchema(ctx)
}

// Plan runs `terraform plan -out=<tmp>` and then `terraform show -json
// <tmp>`. Returns the parsed plan plus a `hasChanges` boolean and the raw
// human-readable output captured during the plan.
func (t *Terraform) Plan(ctx context.Context, planFile string) (*tfjson.Plan, bool, error) {
	hasChanges, err := t.tf.Plan(ctx, tfexec.Out(planFile))
	if err != nil {
		return nil, false, fmt.Errorf("terraform plan: %w", err)
	}
	plan, err := t.tf.ShowPlanFile(ctx, planFile)
	if err != nil {
		return nil, hasChanges, fmt.Errorf("terraform show -json: %w", err)
	}
	return plan, hasChanges, nil
}

// Apply runs `terraform apply <planFile>` using a previously saved plan.
func (t *Terraform) Apply(ctx context.Context, planFile string) error {
	return t.tf.Apply(ctx, tfexec.DirOrPlan(planFile))
}

// Output runs `terraform output -json` and returns the parsed output map.
func (t *Terraform) Output(ctx context.Context) (map[string]tfexec.OutputMeta, error) {
	return t.tf.Output(ctx)
}

// SetEnv configures additional environment variables on the underlying
// tfexec runner.
func (t *Terraform) SetEnv(env map[string]string) error {
	return t.tf.SetEnv(env)
}
