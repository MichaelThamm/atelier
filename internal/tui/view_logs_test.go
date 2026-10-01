package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MichaelThamm/atelier/internal/tfexec"
)

// shortTempDir returns a short-path temp directory (t.TempDir embeds the test
// name, which can push the banner past the width under test) and cleans it up.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "atelier-log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// touchLog creates a wrapper's .atelier/logs/<name> so the banner treats it as
// present on disk.
func touchLog(t *testing.T, wrapperDir, name string) {
	t.Helper()
	dir := filepath.Join(wrapperDir, tfexec.LogDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRenderLogsView_showsAbsoluteLogPath guards the discoverability fix: the
// logs view must tell the user where the persistent terraform diagnostics live,
// as an absolute path, instead of leaving them to guess (or read the README).
// It must name only files that exist — the trace log is opt-in.
func TestRenderLogsView_showsAbsoluteLogPath(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.WrapperDir = shortTempDir(t)
	touchLog(t, m.WrapperDir, tfexec.StderrLogName)
	touchLog(t, m.WrapperDir, tfexec.StdoutLogName)
	m.progress = NewProgressTracker()
	m.progress.AppendStderrLine("Error: something broke")
	m.activeView = viewLogs

	got := stripANSI(m.View())

	wantDir := tfexec.LogDirPath(m.WrapperDir)
	if !strings.Contains(got, wantDir) {
		t.Errorf("logs view missing absolute log dir %q:\n%s", wantDir, got)
	}
	for _, name := range []string{tfexec.StderrLogName, tfexec.StdoutLogName} {
		if !strings.Contains(got, name) {
			t.Errorf("logs view missing existing log file %q:\n%s", name, got)
		}
	}
	if strings.Contains(got, tfexec.TraceLogName) {
		t.Errorf("logs view names %s even though it does not exist:\n%s", tfexec.TraceLogName, got)
	}
}

// TestRenderLogsView_bannerDroppedOnShortPanel keeps the log content from being
// crowded out on a terminal too short to fit the tab bar, the path banner, and
// at least one content line.
func TestRenderLogsView_bannerDroppedOnShortPanel(t *testing.T) {
	m := New(sampleState(t), "cos_lite")
	m = feed(m, tea.WindowSizeMsg{Width: 80, Height: 10})
	m.WrapperDir = shortTempDir(t)
	touchLog(t, m.WrapperDir, tfexec.StderrLogName)
	m.progress = NewProgressTracker()
	m.progress.AppendStderrLine("Error: something broke")
	m.activeView = viewLogs

	got := stripANSI(m.View())
	if strings.Contains(got, "Files:") {
		t.Errorf("banner should be dropped on a short panel:\n%s", got)
	}
	if !strings.Contains(got, "Error: something broke") {
		t.Errorf("log content should still render:\n%s", got)
	}
}

func TestLogFilesBanner(t *testing.T) {
	cases := []struct {
		name    string
		width   int
		files   []string // files to create in the wrapper's log dir
		noDir   bool     // leave WrapperDir empty
		want    []string
		notWant []string
	}{
		{
			name:    "names only files that exist",
			width:   120,
			files:   []string{tfexec.StderrLogName, tfexec.StdoutLogName},
			want:    []string{"Files: ", "/.atelier/logs/", tfexec.StderrLogName, tfexec.StdoutLogName},
			notWant: []string{tfexec.TraceLogName},
		},
		{
			name:  "includes trace when debug wrote it",
			width: 120,
			files: []string{tfexec.StderrLogName, tfexec.StdoutLogName, tfexec.TraceLogName},
			want:  []string{tfexec.StderrLogName, tfexec.StdoutLogName, tfexec.TraceLogName},
		},
		{
			name:  "long path keeps the filename list and truncates the path",
			width: 60,
			files: []string{tfexec.StderrLogName, tfexec.StdoutLogName},
			want:  []string{"Files: ", "/tmp", tfexec.StderrLogName, tfexec.StdoutLogName},
		},
		{
			name:    "narrow drops the filename suffix",
			width:   40,
			files:   []string{tfexec.StderrLogName},
			want:    []string{"Files: ", "/tmp"},
			notWant: []string{tfexec.StderrLogName, tfexec.StdoutLogName, tfexec.TraceLogName},
		},
		{
			name:    "unknown wrapper dir yields no banner",
			width:   120,
			noDir:   true,
			notWant: []string{"Files:"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(sampleState(t), "cos_lite")
			m.width = tc.width
			if !tc.noDir {
				// A real directory so os.Stat sees the files (shortTempDir
				// lives under /tmp, which the narrow case relies on).
				m.WrapperDir = shortTempDir(t)
			}
			for _, name := range tc.files {
				touchLog(t, m.WrapperDir, name)
			}
			got := m.logFilesBanner()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("logFilesBanner() = %q; want it to contain %q", got, want)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("logFilesBanner() = %q; want it not to contain %q", got, notWant)
				}
			}
		})
	}
}
