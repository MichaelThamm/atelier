package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Terraform operation budgets. `init` is network-scale — a cold cache fetches
// every module and provider, which on a constrained host can take many minutes —
// so it gets its own budget and is never bounded by the short validate budget or
// the plan budget. See ADR-0063.
const (
	initTimeout     = 15 * time.Minute
	planTimeout     = 5 * time.Minute
	validateTimeout = 30 * time.Second
)

// startPlan composes the async pipeline behind the P key: save state, run
// `terraform init` if needed, run `terraform plan`, return the parsed plan.
// Errors are funnelled through planErrorMsg so the UI can surface them
// without dropping the editor session.
func (m *Model) startPlan() tea.Cmd {
	if m.Planner == nil {
		return func() tea.Msg {
			return planErrorMsg{err: fmt.Errorf("plan unavailable: planner not configured")}
		}
	}
	m.beginOp("Running terraform plan…")
	modules := m.Modules
	planner := m.Planner
	return func() tea.Msg {
		if err := writeAllModules(modules); err != nil {
			return planErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
		}
		initCtx, cancelInit := context.WithTimeout(context.Background(), initTimeout)
		defer cancelInit()
		if err := planner.EnsureInit(initCtx); err != nil {
			return planErrorMsg{err: err}
		}
		planCtx, cancelPlan := context.WithTimeout(context.Background(), planTimeout)
		defer cancelPlan()
		plan, err := planner.Plan(planCtx)
		if err != nil {
			return planErrorMsg{err: err}
		}
		return planResultMsg{plan: plan}
	}
}

// beginOp labels an in-flight terraform operation so the footer spinner can
// show it and time it.
func (m *Model) beginOp(phase string) {
	m.opPhase = phase
	m.opStarted = time.Now()
}

// spinnerTick schedules the next animation frame. Cheap: a single timer.
func spinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// startApply applies the cached plan file. The command it returns hands the
// terminal back to terraform, so this only has to mark the flow in flight;
// the outcome arrives as applyResultMsg/applyErrorMsg from the callback.
func (m *Model) startApply() tea.Cmd {
	if m.Applier == nil {
		return func() tea.Msg {
			return applyErrorMsg{err: fmt.Errorf("apply unavailable: applier not configured")}
		}
	}
	m.beginOp("Running terraform apply…")
	return m.Applier.ApplyCmd()
}

// scheduleValidate bumps the generation counter and returns a debounce
// tick command. Called after every edit.
func (m *Model) scheduleValidate() tea.Cmd {
	if m.Validator == nil {
		return nil
	}
	m.validateGen++
	gen := m.validateGen
	return tea.Tick(500*time.Millisecond, func(_ time.Time) tea.Msg {
		return validateDebounceMsg{gen: gen}
	})
}

// startValidate saves state and runs `terraform validate` asynchronously. The
// `init` that validate needs runs first on the network-scale budget, so a cold
// cache is not killed by validate's short one; `terraform validate` itself is
// local and quick. See ADR-0063.
func (m *Model) startValidate() tea.Cmd {
	modules := m.Modules
	validator := m.Validator
	planner := m.Planner
	return func() tea.Msg {
		if err := writeAllModules(modules); err != nil {
			return validateErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
		}
		if planner != nil {
			initCtx, cancelInit := context.WithTimeout(context.Background(), initTimeout)
			defer cancelInit()
			if err := planner.EnsureInit(initCtx); err != nil {
				return validateErrorMsg{err: err}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
		defer cancel()
		out, err := validator.Validate(ctx)
		if err != nil {
			return validateErrorMsg{err: err}
		}
		return validateResultMsg{output: out}
	}
}

// startRefSwitch runs the ref switch in a goroutine and returns result/error
// messages to the TUI.
func (m *Model) startRefSwitch(newRef string) tea.Cmd {
	m.beginOp("Switching ref…")
	switcher := m.switcherForIdx(m.refModuleIdx)
	return func() tea.Msg {
		// A ref switch re-clones the module and runs `init -upgrade`, so it
		// shares init's network-scale budget (ADR-0063).
		ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
		defer cancel()
		result, err := switcher.SwitchRef(ctx, newRef)
		if err != nil {
			return refSwitchErrorMsg{err: err}
		}
		return refSwitchResultMsg{result: result}
	}
}

// startListRefs fetches the remote's available ref names for the module the
// modal targets, off the UI thread. Failures are swallowed to a nil list —
// the hint simply won't render, and the input stays free-text.
func (m *Model) startListRefs() tea.Cmd {
	switcher := m.switcherForIdx(m.refModuleIdx)
	if switcher == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		refs, err := switcher.ListRefs(ctx)
		if err != nil {
			return refsLoadedMsg{refs: nil}
		}
		return refsLoadedMsg{refs: refs}
	}
}
