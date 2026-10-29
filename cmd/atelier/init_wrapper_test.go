package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/tfexec"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

func TestWrapperDeclarationsChanged(t *testing.T) {
	blocks := func(pairs ...string) []wrapper.ModuleBlockInfo {
		var out []wrapper.ModuleBlockInfo
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, wrapper.ModuleBlockInfo{Name: pairs[i], Source: pairs[i+1]})
		}
		return out
	}
	cases := []struct {
		name          string
		before, after []wrapper.ModuleBlockInfo
		want          bool
	}{
		{"same block, same ref", blocks("a", "git::u//m?ref=v1"), blocks("a", "git::u//m?ref=v1"), false},
		{"same block, re-pointed ref", blocks("a", "git::u//m?ref=v1"), blocks("a", "git::u//m?ref=v2"), true},
		{"appended block", blocks("a", "git::u//m"), blocks("a", "git::u//m", "b", "git::v//n"), true},
		{"unreadable before reads as changed", nil, blocks("a", "git::u//m"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wrapperDeclarationsChanged(c.before, c.after); got != c.want {
				t.Errorf("wrapperDeclarationsChanged() = %v; want %v", got, c.want)
			}
		})
	}
}

// TestInitWrapper_upgradesOnlyWhenChanged pins the ADR-0062 contract at the
// command boundary: a re-deploy at the same ref runs a plain init, `-upgrade`
// only when the compose changed the wrapper, and a plain init Terraform refuses
// is retried with `-upgrade`.
func TestInitWrapper_upgradesOnlyWhenChanged(t *testing.T) {
	cases := []struct {
		name      string
		upgrade   bool
		failPlain bool
		wantLines []string // substrings, one per expected init invocation, in order
	}{
		{"unchanged runs plain init only", false, false, []string{"-upgrade=false"}},
		{"changed runs init -upgrade", true, false, []string{"-upgrade=true"}},
		{"refused plain init retries with -upgrade", false, true, []string{"-upgrade=false", "-upgrade=true"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "argv.log")
			script := filepath.Join(dir, "fake-terraform")
			fail := "no"
			if c.failPlain {
				fail = "yes"
			}
			// The fake answers the version query terraform-exec makes before init
			// (`version -json`), records every init's argv, and fails a plain init
			// when asked — modelling the lock-file refusal that needs -upgrade.
			body := "#!/bin/sh\n" +
				"if [ \"$1\" = version ]; then echo '{\"terraform_version\":\"1.9.0\",\"platform\":\"linux_amd64\",\"provider_selections\":{},\"terraform_outdated\":false}'; exit 0; fi\n" +
				"printf '%s\\n' \"$*\" >> " + shellQuote(logPath) + "\n" +
				"case \"$*\" in *-upgrade=true*) exit 0;; esac\n" +
				"if [ " + shellQuote(fail) + " = yes ]; then echo 'Error: inconsistent dependency lock file; run terraform init -upgrade' >&2; exit 1; fi\n" +
				"exit 0\n"
			if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			tf, err := tfexec.New(dir, script)
			if err != nil {
				t.Fatal(err)
			}
			if err := initWrapper(context.Background(), tf, c.upgrade); err != nil {
				t.Fatalf("initWrapper: %v", err)
			}

			got, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("read argv log: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(string(got)), "\n")
			if len(lines) != len(c.wantLines) {
				t.Fatalf("init ran %d time(s) (%q); want %d", len(lines), string(got), len(c.wantLines))
			}
			for i, want := range c.wantLines {
				if !strings.Contains(lines[i], want) {
					t.Errorf("init #%d = %q; want it to contain %q", i+1, lines[i], want)
				}
			}
		})
	}
}

// shellQuote single-quotes s for a /bin/sh command, escaping embedded quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
