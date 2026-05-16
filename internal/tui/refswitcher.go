package tui

import (
	"context"

	"github.com/canonical/atelier/internal/tfvars"
	"github.com/canonical/atelier/internal/wrapper"
)

// RefSwitcher is the narrow interface the TUI needs to switch the module ref.
// Defined as an interface so tests can substitute a stub.
type RefSwitcher interface {
	// SwitchRef re-clones the module at the new ref, re-parses variables,
	// runs terraform init -upgrade, and returns the updated state. The
	// caller is responsible for merging user overrides from the old state.
	SwitchRef(ctx context.Context, newRef string) (*RefSwitchResult, error)
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
}
