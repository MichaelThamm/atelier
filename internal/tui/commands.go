package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := planner.EnsureInit(ctx); err != nil {
			return planErrorMsg{err: err}
		}
		plan, err := planner.Plan(ctx)
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

// startValidate saves state and runs `terraform validate` asynchronously.
func (m *Model) startValidate() tea.Cmd {
	modules := m.Modules
	validator := m.Validator
	return func() tea.Msg {
		if err := writeAllModules(modules); err != nil {
			return validateErrorMsg{err: fmt.Errorf("save wrapper: %w", err)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
