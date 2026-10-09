package tfexec

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ActionLog bounds and names one terraform action's output in a wrapper's
// durable log files (.atelier/logs/tf-stdout.log and tf-stderr.log). The
// stdout and stderr halves share one header, so a reader can pair them; the
// header is written only when the action actually writes to that file, and an
// end marker closes the block. ANSI escapes are stripped from what reaches
// the files, so they stay plain text. See ADR-0056.
type ActionLog struct {
	out    *logFile
	err    *logFile
	label  string // e.g. "terraform apply (/path/to/wrapper)"
	closed bool
}

// BeginAction starts logging one terraform action (init, plan, apply). The
// command names the terraform subcommand and its relevant flags (e.g.
// "init -upgrade"); it appears in the log header alongside the resolved
// binary and wrapper directory. Callers install the returned writers as
// terraform's stdout and stderr and call Close when the action finishes.
//
// The two log files share the same header string, including one start
// timestamp, so their blocks pair up. A file that receives no output gets
// neither a header nor an end marker, so an empty action cannot leave an
// orphan header behind.
func (t *Terraform) BeginAction(command string) *ActionLog {
	bin := filepath.Base(t.binPath)
	if bin == "" || bin == "." {
		bin = "terraform"
	}
	workdir := t.workdir
	if abs, err := filepath.Abs(workdir); err == nil {
		workdir = abs
	}
	label := fmt.Sprintf("%s %s (%s)", bin, command, workdir)
	header := fmt.Sprintf("=== %s %s ===", time.Now().Format(time.RFC3339), label)
	return &ActionLog{
		out:   &logFile{file: t.stdoutFile, header: header},
		err:   &logFile{file: t.stderrFile, header: header},
		label: label,
	}
}

// Stdout returns the sink for the action's stdout: the durable stdout log,
// with ANSI stripped, plus mirror (the user's terminal) when non-nil. The log
// is written first so a broken terminal cannot swallow the durable copy.
func (a *ActionLog) Stdout(mirror io.Writer) io.Writer {
	if a == nil {
		return mirrorOrDiscard(mirror)
	}
	return a.out.writer(mirror)
}

// Stderr is Stdout for the action's stderr and tf-stderr.log.
func (a *ActionLog) Stderr(mirror io.Writer) io.Writer {
	if a == nil {
		return mirrorOrDiscard(mirror)
	}
	return a.err.writer(mirror)
}

func (l *logFile) writer(mirror io.Writer) io.Writer {
	if l == nil || l.file == nil {
		return mirrorOrDiscard(mirror)
	}
	if mirror == nil {
		return l
	}
	return io.MultiWriter(l, mirror)
}

func mirrorOrDiscard(mirror io.Writer) io.Writer {
	if mirror == nil {
		return io.Discard
	}
	return mirror
}

// Close writes the end marker to each log file that received output. It is
// idempotent, so a caller may defer it and also call it on an early return.
func (a *ActionLog) Close() {
	if a == nil || a.closed {
		return
	}
	a.closed = true
	end := fmt.Sprintf("=== %s %s finished ===", time.Now().Format(time.RFC3339), a.label)
	for _, l := range []*logFile{a.out, a.err} {
		if l != nil && l.file != nil && l.wrote {
			_, _ = io.WriteString(l.file, "\n"+end+"\n")
		}
	}
}

// logFile is one half of an ActionLog. It writes the shared header on the
// first byte it receives (never before), strips ANSI escapes, and appends to
// the file.
type logFile struct {
	file   *os.File
	header string
	wrote  bool
	ansi   ansiStripper
}

func (l *logFile) Write(p []byte) (int, error) {
	// The io.Writer contract is to report len(p): escape bytes are consumed
	// even though they are not written.
	clean := l.ansi.strip(p)
	if len(clean) == 0 {
		return len(p), nil
	}
	if !l.wrote {
		if _, err := io.WriteString(l.file, "\n"+l.header+"\n"); err != nil {
			return 0, err
		}
		l.wrote = true
	}
	if _, err := l.file.Write(clean); err != nil {
		return 0, err
	}
	return len(p), nil
}

// ansiStripper removes ANSI escape sequences from a byte stream. It keeps its
// state across Write calls because a sequence can be split across two writes.
// It copies rather than filtering in place so a tee'd terminal still receives
// the original bytes.
type ansiStripper struct {
	state ansiState
}

type ansiState int

const (
	ansiNormal ansiState = iota
	ansiEsc
	ansiCSI
	ansiOSC
	ansiOSCEsc
)

func (s *ansiStripper) strip(p []byte) []byte {
	out := make([]byte, 0, len(p))
	for _, b := range p {
		switch s.state {
		case ansiNormal:
			if b == 0x1b {
				s.state = ansiEsc
			} else {
				out = append(out, b)
			}
		case ansiEsc:
			switch b {
			case '[':
				s.state = ansiCSI
			case ']':
				s.state = ansiOSC
			default:
				// A two-byte escape (e.g. ESC ( B) or a stray ESC: drop the
				// sequence and move on.
				s.state = ansiNormal
			}
		case ansiCSI:
			// Parameters/intermediates precede a final byte in 0x40–0x7e.
			if b >= 0x40 && b <= 0x7e {
				s.state = ansiNormal
			}
		case ansiOSC:
			switch b {
			case 0x07: // BEL terminator
				s.state = ansiNormal
			case 0x1b: // possible ST (ESC \)
				s.state = ansiOSCEsc
			}
		case ansiOSCEsc:
			if b == '\\' {
				s.state = ansiNormal
			} else if b != 0x1b {
				s.state = ansiOSC
			}
		}
	}
	return out
}
