package tui

import (
	"context"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// RefSwitcher is the narrow interface the TUI needs to switch the module ref.
// Defined as an interface so tests can substitute a stub.
type RefSwitcher interface {
	// SwitchRef re-clones the module at the new ref, re-parses variables,
	// runs terraform init -upgrade, and returns the updated state. The
	// caller is responsible for merging user overrides from the old state.
	SwitchRef(ctx context.Context, newRef string) (*RefSwitchResult, error)
}

// ProgressAware is an optional interface that RefSwitcher implementations can
// satisfy to receive a ProgressTracker before long-running operations. The TUI
// checks for this at runtime and attaches a tracker when available.
type ProgressAware interface {
	SetProgress(p *ProgressTracker)
}

// RefSwitchResult is returned by SwitchRef with the new state, any orphaned
// variable names (present in old state but absent in new module), and the
// resolved SHA.
type RefSwitchResult struct {
	State       *wrapper.State
	ResolvedSHA string
	LiteralRef  string
	// OrphanedVars lists variable names that had user-set values in the
	// previous state but no longer exist in the module at the new ref.
	OrphanedVars []string
	// NewVars lists variables added in the new ref that were not present
	// in the previous state.
	NewVars []tfvars.Variable
	// InitIncomplete is true when the post-switch `terraform init -upgrade`
	// did not finish cleanly. The switch still succeeds: the module is
	// fetched (module installation precedes config validation) and the new
	// schema is shown. The most common cause — the new ref added a required
	// variable that isn't filled yet — is detected and phrased by the TUI
	// from its own state, so this flag only needs to convey the bare signal.
	// The planner re-runs init -upgrade on the next plan, and
	// `terraform validate` surfaces the specific diagnostics in the meantime.
	InitIncomplete bool
}
