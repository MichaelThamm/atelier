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
	// Create a fresh progress tracker and attach it to the planner.
	m.progress = NewProgressTracker()
	if tp, ok := m.Planner.(*TfexecPlanner); ok {
		tp.Progress = m.progress
	}
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

// spinnerTick schedules the next animation frame. Cheap: a single timer.
func spinnerTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
		return spinnerTickMsg(t)
	})
}

// startApply runs `terraform apply` using the cached plan file.
func (m *Model) startApply() tea.Cmd {
	if m.Applier == nil {
		return func() tea.Msg {
			return applyErrorMsg{err: fmt.Errorf("apply unavailable: applier not configured")}
		}
	}
	// Create a fresh progress tracker and attach it to the applier.
	m.progress = NewProgressTracker()
	if tp, ok := m.Applier.(*TfexecPlanner); ok {
		tp.Progress = m.progress
	}
	applier := m.Applier
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := applier.Apply(ctx); err != nil {
			return applyErrorMsg{err: err}
		}
		return applyResultMsg{}
	}
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
	m.progress = NewProgressTracker()
	switcher := m.switcherForIdx(m.refModuleIdx)
	if pa, ok := switcher.(ProgressAware); ok {
		pa.SetProgress(m.progress)
	}
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
