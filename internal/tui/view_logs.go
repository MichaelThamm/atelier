package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// renderLogsView renders a scrollable panel of live terraform output with
// tabs for stderr (errors) and stdout (logs).
func (m *Model) renderLogsView() string {
	if m.progress == nil {
		return stylePanel.Width(m.width - 2).Height(m.panelHeight()).
			Render(styleDescription.Render("No logs available."))
	}

	// Get lines based on active tab
	var lines []LogLine
	switch m.logsTab {
	case logsTabErrors:
		lines = m.progress.StderrLines()
	case logsTabLogs:
		lines = m.progress.StdoutLines()
	}

	// Format lines (no per-line timestamps - action start time shown in footer)
	formattedLines := make([]string, len(lines))
	for i, l := range lines {
		formattedLines[i] = l.Content
	}

	// Banner naming the absolute log directory. It is dropped on panels too
	// short to spare the line, so the log content keeps at least one row.
	banner := ""
	if m.panelHeight() >= 3 {
		banner = m.logFilesBanner()
	}
	headerLines := 1 // tab bar
	if banner != "" {
		headerLines++
	}

	h := m.panelHeight() - headerLines
	if h < 1 {
		h = 1
	}
	// Clamp scroll and height to valid range.
	if len(formattedLines) == 0 {
		m.logScroll = 0
		h = 0
	} else if len(formattedLines) <= h {
		m.logScroll = 0
		h = len(formattedLines)
	} else if m.logAutoScroll {
		m.logScroll = len(formattedLines) - h
	} else if m.logScroll > len(formattedLines)-h {
		m.logScroll = len(formattedLines) - h
	}
	if m.logScroll < 0 {
		m.logScroll = 0
	}

	var content string
	if h > 0 {
		visible := formattedLines[m.logScroll : m.logScroll+h]
		content = strings.Join(visible, "\n")
	}

	// Empty state message
	if content == "" {
		switch m.logsTab {
		case logsTabErrors:
			content = styleDescription.Render("No errors captured.")
		case logsTabLogs:
			content = styleDescription.Render("No logs captured.")
		}
	}
	content = clampToLines(content, h)

	// Build tab bar with action start time
	errorCount := len(m.progress.StderrLines())
	logCount := len(m.progress.StdoutLines())
	errorLabel := fmt.Sprintf("Errors (%d)", errorCount)
	logLabel := fmt.Sprintf("Logs (%d)", logCount)

	var tabBar strings.Builder
	if m.logsTab == logsTabErrors {
		tabBar.WriteString(styleTabActive.Render(errorLabel))
		tabBar.WriteString("  ")
		tabBar.WriteString(logLabel)
	} else {
		tabBar.WriteString(errorLabel)
		tabBar.WriteString("  ")
		tabBar.WriteString(styleTabActive.Render(logLabel))
	}

	// Add action start time
	startTime := m.progress.StartTime().Format("15:04:05")
	tabBar.WriteString(styleDescription.Render(fmt.Sprintf("  (started %s)", startTime)))

	// Combine tab bar, log-path banner, and content.
	head := tabBar.String()
	if banner != "" {
		head += "\n" + styleDescription.Render(banner)
	}
	return stylePanelFocused.Width(m.width - 2).Height(m.panelHeight()).
		Render(head + "\n" + content)
}

// logFilesBanner returns the one-line hint naming the wrapper's persistent
// terraform diagnostics directory and the files that exist in it, or "" when
// the wrapper directory is unknown. The directory is absolute so it can be
// opened outside the TUI. Only files actually on disk are named: tf-stderr.log
// and tf-stdout.log are created eagerly, but tf-trace.log appears only when
// ATELIER_DEBUG is set, so naming it unconditionally would be a lie.
//
// When the line does not fit, the path middle-truncates (keeping its root and
// tail) before the filename list is dropped, so the more specific information
// survives the longest.
func (m *Model) logFilesBanner() string {
	if m.WrapperDir == "" {
		return ""
	}
	const (
		prefix = "Files: "
		// Below this, a middle-truncated path reads as noise, so drop the
		// filename list instead and spend the whole line on the path.
		minPathWidth = 16
	)
	dir := tfexec.LogDirPath(m.WrapperDir)

	var names []string
	for _, name := range []string{tfexec.StderrLogName, tfexec.StdoutLogName, tfexec.TraceLogName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			names = append(names, name)
		}
	}
	suffix := ""
	if len(names) > 0 {
		suffix = "  (" + strings.Join(names, " · ") + ")"
	}

	innerW := m.width - 4 // panel border (2) + padding (2)
	if innerW < 1 {
		return ""
	}
	pathBudget := innerW - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	if suffix != "" && pathBudget < minPathWidth {
		suffix = ""
		pathBudget = innerW - lipgloss.Width(prefix)
	}
	if pathBudget < 1 {
		return ""
	}
	return prefix + truncateMiddle(dir+"/", pathBudget) + suffix
}

// progressSuffix returns a formatted string with elapsed time and current
// phase from the progress tracker, e.g. " (12s) Refreshing state…". Returns
// "" if no progress tracker is active.
func (m *Model) progressSuffix() string {
	if m.progress == nil {
		return ""
	}
	elapsed := m.progress.Elapsed().Truncate(time.Second)
	return fmt.Sprintf(" (%s)", formatDuration(elapsed))
}

// formatDuration renders a duration as a compact human string: "3s", "1m12s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) - m*60
	return fmt.Sprintf("%dm%ds", m, s)
}

// renderStatus composes the full footer (kept for plan view compatibility).
func (m *Model) renderStatus() string {
	return m.renderFooter()
}

func (m *Model) statusHints() string {
	if m.height < 15 {
		return "[?] help"
	}
	if m.activeView == viewLogs {
		return "[" + arrowUpDown + "] scroll  [Tab] switch tab  [L/Esc] back  [?] help"
	}
	switch m.planState {
	case planLoading:
		return "[L] logs  [Esc] cancel  [?] help"
	case planReady:
		if m.planDiffFocus {
			return "[" + arrowUpDown + "] scroll diff  [Tab/Esc] back to tree  [?] help"
		}
		hints := "[" + arrowUpDown + "] navigate  [Enter] toggle  [Tab] focus diff  [P] re-plan"
		if m.tfState != nil {
			if m.planShowState {
				hints += "  [S] show diff"
			} else {
				hints += "  [S] show state"
			}
		}
		if m.Applier != nil && m.applyState != applyLoading {
			hints += "  [A] apply"
		}
		if m.progress != nil && (len(m.progress.StderrLines()) > 0 || len(m.progress.StdoutLines()) > 0) {
			hints += "  [L] logs"
		}
		if len(m.checkWarnings) > 0 {
			hints += "  [W] warnings"
		}
		if m.refDetailText != "" {
			hints += "  [D] details"
		}
		hints += "  [Esc] back  [?] help"
		return hints
	}
	hints := "[Tab] pane  [" + arrowUpDown + "] navigate  [P] plan"
	if len(m.presets) > 0 {
		hints += "  [F] preset"
	}
	hints += "  [S] save"
	if m.activeSwitcher() != nil {
		hints += "  [R] ref"
	}
	if m.progress != nil && (len(m.progress.StderrLines()) > 0 || len(m.progress.StdoutLines()) > 0) {
		hints += "  [L] logs"
	}
	if m.refDetailText != "" {
		hints += "  [D] details"
	}
	hints += "  [Q] quit  [?] help"
	return hints
}
