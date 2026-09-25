package tui

import (
	"time"

	tfjson "github.com/hashicorp/terraform-json"
)

// planResultMsg carries a successful plan back to the UI thread.
type planResultMsg struct {
	plan *tfjson.Plan
}

// planErrorMsg carries a plan failure back to the UI thread for display
// in the status bar.
type planErrorMsg struct {
	err error
}

// spinnerTickMsg drives the in-flight spinner animation.
type spinnerTickMsg time.Time

// refSwitchResultMsg carries a successful ref switch back to the UI thread.
type refSwitchResultMsg struct {
	result *RefSwitchResult
}

// refSwitchErrorMsg carries a ref-switch failure back to the UI thread.
type refSwitchErrorMsg struct {
	err error
}

// refsLoadedMsg carries the result of an async ListRefs fetch for the
// ref-switch modal hint. refs is nil when the remote couldn't be reached
// (non-fatal — the input stays free-text).
type refsLoadedMsg struct {
	refs []string
}

// applyResultMsg signals a successful terraform apply.
type applyResultMsg struct{}

// applyErrorMsg carries an apply failure back to the UI thread.
type applyErrorMsg struct {
	err error
}

// validateDebounceMsg fires after the debounce delay. The gen field is
// compared against Model.validateGen to detect stale ticks.
type validateDebounceMsg struct {
	gen uint64
}

// validateResultMsg carries a successful validate result back to the UI.
type validateResultMsg struct {
	output *tfjson.ValidateOutput
}

// validateErrorMsg carries a validate failure back to the UI.
type validateErrorMsg struct {
	err error
}
