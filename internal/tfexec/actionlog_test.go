package tfexec

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readLogs(t *testing.T, dir string) (stdout, stderr string) {
	t.Helper()
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, LogDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(b)
	}
	return read(StdoutLogName), read(StderrLogName)
}

// TestActionLog_lazyHeaderBoundTheBlock pins the format: the header names the
// terraform action and wrapper directory and carries an RFC3339 timestamp, and
// an end marker bounds the block. An action that writes nothing must leave no
// header (or end marker) behind.
func TestActionLog_lazyHeaderBoundTheBlock(t *testing.T) {
	dir := t.TempDir()
	tf, err := New(dir, "terraform")
	if err != nil {
		t.Fatal(err)
	}

	// An empty action leaves both files untouched.
	empty := tf.BeginAction("plan")
	empty.Close()
	stdout, stderr := readLogs(t, dir)
	if stdout != "" || stderr != "" {
		t.Fatalf("empty action wrote a header: stdout=%q stderr=%q", stdout, stderr)
	}

	action := tf.BeginAction("init -upgrade")
	if _, err := action.Stdout(nil).Write([]byte("installing...\n")); err != nil {
		t.Fatal(err)
	}
	action.Close()
	stdout, stderr = readLogs(t, dir)

	wantHeader := "terraform init -upgrade (" + dir + ") ==="
	if !strings.Contains(stdout, wantHeader) {
		t.Errorf("stdout header missing %q: %q", wantHeader, stdout)
	}
	if !strings.HasPrefix(stdout, "\n=== ") {
		t.Errorf("stdout header not first: %q", stdout)
	}
	if !strings.Contains(stdout, "installing...") {
		t.Errorf("stdout lost the action output: %q", stdout)
	}
	if !strings.Contains(stdout, "terraform init -upgrade ("+dir+") finished ===") {
		t.Errorf("stdout end marker missing: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("stdout-only action wrote stderr: %q", stderr)
	}
}

// TestActionLog_sharedHeaderPairsFiles checks the stdout and stderr halves of
// one action carry the identical header, so a reader can pair them.
func TestActionLog_sharedHeaderPairsFiles(t *testing.T) {
	dir := t.TempDir()
	tf, err := New(dir, "terraform")
	if err != nil {
		t.Fatal(err)
	}
	action := tf.BeginAction("apply")
	if _, err := action.Stdout(nil).Write([]byte("plan-output\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := action.Stderr(nil).Write([]byte("error-output\n")); err != nil {
		t.Fatal(err)
	}
	action.Close()
	stdout, stderr := readLogs(t, dir)

	header := func(s string) string {
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, "terraform apply (") && strings.HasSuffix(line, "===") && !strings.Contains(line, "finished") {
				return line
			}
		}
		return ""
	}
	sh, eh := header(stdout), header(stderr)
	if sh == "" || eh == "" {
		t.Fatalf("missing header: stdout=%q stderr=%q", stdout, stderr)
	}
	if sh != eh {
		t.Errorf("headers differ: stdout=%q stderr=%q", sh, eh)
	}
}

// TestActionLog_stripsANSIFromLogKeepsTerminal checks the log copy is plain
// text while a tee'd terminal still gets the original escapes.
func TestActionLog_stripsANSIFromLogKeepsTerminal(t *testing.T) {
	dir := t.TempDir()
	tf, err := New(dir, "terraform")
	if err != nil {
		t.Fatal(err)
	}
	var terminal strings.Builder
	action := tf.BeginAction("apply")
	if _, err := action.Stdout(&terminal).Write([]byte("\x1b[31mred\x1b[0m\n")); err != nil {
		t.Fatal(err)
	}
	action.Close()
	stdout, _ := readLogs(t, dir)

	if strings.Contains(stdout, "\x1b") {
		t.Errorf("log kept ANSI escapes: %q", stdout)
	}
	if !strings.Contains(stdout, "red") {
		t.Errorf("log lost the text around the escapes: %q", stdout)
	}
	if !strings.Contains(terminal.String(), "\x1b[31m") {
		t.Errorf("terminal lost its color: %q", terminal.String())
	}
}

// TestActionLog_ansiSplitAcrossWrites covers a sequence straddling two writes:
// the strapper must keep its state rather than leaking the tail.
func TestActionLog_ansiSplitAcrossWrites(t *testing.T) {
	dir := t.TempDir()
	tf, err := New(dir, "terraform")
	if err != nil {
		t.Fatal(err)
	}
	action := tf.BeginAction("plan")
	w := action.Stdout(nil)
	if _, err := w.Write([]byte("\x1b[3")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("1mred\x1b[0m\n")); err != nil {
		t.Fatal(err)
	}
	action.Close()
	stdout, _ := readLogs(t, dir)
	if strings.Contains(stdout, "[31m") || strings.Contains(stdout, "\x1b") {
		t.Errorf("split escape leaked into the log: %q", stdout)
	}
	if !strings.Contains(stdout, "red") {
		t.Errorf("log lost the text: %q", stdout)
	}
}

// TestActionLog_noLoggingPassthrough covers a Terraform whose log handles were
// never configured: Stdout(nil) discards, and a mirror still receives the
// bytes rather than being dropped behind a nil file.
func TestActionLog_noLoggingPassthrough(t *testing.T) {
	tf := &Terraform{binPath: "terraform", workdir: t.TempDir()}
	action := tf.BeginAction("plan")
	if w := action.Stdout(nil); w != io.Discard {
		t.Errorf("Stdout(nil) with no log = %T; want io.Discard", w)
	}
	var buf strings.Builder
	if _, err := action.Stdout(&buf).Write([]byte("to-terminal\n")); err != nil {
		t.Fatal(err)
	}
	action.Close() // must not panic on nil files
	if buf.String() != "to-terminal\n" {
		t.Errorf("mirror did not receive output: %q", buf.String())
	}
}
