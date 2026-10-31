package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every command answers -h and --help by printing its own help, and answers it
// the same way wherever the flag appears. Help is the only discovery path this
// CLI has for the flags a command takes, so it has to be reachable by the
// obvious guess on every command, and it must describe that command rather
// than the whole tool.
func TestEveryCommand_answersHelpFlags(t *testing.T) {
	// Run from a directory holding a real wrapper: a command that fell through
	// to its body would succeed here and print its own output instead of help,
	// which is exactly the bug being pinned.
	dir := t.TempDir()
	main := `module "demo" {
  source = "git::https://github.com/o/r.git//terraform?ref=v1"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })

	commands := []struct {
		name string
		run  func([]string) error
		want string // the help the command must print, and nothing else
	}{
		{"add", runModuleAdd, addUsage},
		{"rm", runModuleRm, rmUsage},
		{"ls", runModuleList, lsUsage},
		{"apply", runModuleApply, applyUsage},
		{"wrappers", runWrappers, wrappersUsage},
		{"purge", runPurge, purgeUsage},
		{"import", runImport, importUsage},
		{"presets", runPresets, presetsUsage},
		{"gallery", runGallery, galleryUsage},
		// Sub-subcommands reached through a namespace.
		{"gallery list", runGalleryList, galleryUsage},
		{"gallery lint", runGalleryLint, galleryUsage},
		{"gallery requires", runGalleryRequires, galleryUsage},
		{"presets lint", runPresetsLint, presetsUsage},
	}

	for _, c := range commands {
		for _, flag := range []string{"--help", "-h"} {
			t.Run(c.name+" "+flag, func(t *testing.T) {
				// Both bare and trailing a positional, since a user
				// who has already typed the module URL is the likelier
				// one to reach for help.
				for _, args := range [][]string{{flag}, {"example.com/mod", flag}} {
					out, err := captureStdout(t, func() error { return c.run(args) })
					if err != nil {
						t.Fatalf("atelier %s %v: %v", c.name, args, err)
					}
					// Exactly the command's help, so a command that fell
					// through to its body is caught: `ls` prints a module
					// table here.
					if out != c.want {
						t.Errorf("atelier %s %v printed %d bytes, want the %d bytes of its help:\n%.200s",
							c.name, args, len(out), len(c.want), out)
					}
				}
			})
		}
	}
}

// Dispatch must reach each command's own handler before help is answered, so
// that `atelier add --help` prints add's help rather than the top-level usage.
// The per-command table above calls the run functions directly and cannot see
// this layer.
func TestRun_helpReachesTheDispatchedCommand(t *testing.T) {
	scoped := map[string]string{
		"add":          addUsage,
		"rm":           rmUsage,
		"ls":           lsUsage,
		"apply":        applyUsage,
		"wrappers":     wrappersUsage,
		"purge":        purgeUsage,
		"import":       importUsage,
		"gallery":      galleryUsage,
		"gallery list": galleryUsage,
		"presets":      presetsUsage,
		"presets lint": presetsUsage,
	}
	for cmd, want := range scoped {
		t.Run(cmd, func(t *testing.T) {
			words := strings.Fields(cmd)
			for _, flag := range []string{"--help", "-h"} {
				out, err := captureStdout(t, func() error {
					return run(append(words, flag))
				})
				if err != nil {
					t.Fatalf("atelier %s %s: %v", cmd, flag, err)
				}
				if out != want {
					t.Errorf("atelier %s %s printed the wrong help (%d bytes, want %d):\n%.120s",
						cmd, flag, len(out), len(want), out)
				}
			}
		})
	}
}

// `atelier help [command]` is the standard's help entrypoint. It prints the
// top-level usage with no topic, a command's own help with one, and fails on an
// unknown topic. It accepts the same aliases the dispatcher does.
func TestRun_helpCommand(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "help"}} {
		out, err := captureStdout(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if out != usage {
			t.Errorf("run(%v) did not print the top-level usage", args)
		}
	}

	topics := map[string]string{
		"add":     addUsage,
		"remove":  rmUsage, // alias of rm
		"list":    lsUsage, // alias of ls
		"apply":   applyUsage,
		"import":  importUsage,
		"purge":   purgeUsage,
		"gallery": galleryUsage,
	}
	for topic, want := range topics {
		out, err := captureStdout(t, func() error { return run([]string{"help", topic}) })
		if err != nil {
			t.Fatalf("run(help %s): %v", topic, err)
		}
		if out != want {
			t.Errorf("run(help %s) printed %d bytes, want %d:\n%.120s", topic, len(out), len(want), out)
		}
	}

	if err := run([]string{"help", "nope"}); err == nil {
		t.Error("run(help nope) accepted an unknown topic")
	}
}

// The namespace and top-level help must stay reachable, and --version must not
// be swallowed by the help check that scans every argument.
func TestRun_topLevelHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		out, err := captureStdout(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if !strings.Contains(out, "Usage:") || !strings.Contains(out, "atelier add ") {
			t.Errorf("run(%v) did not print the top-level usage:\n%s", args, out)
		}
	}
	out, err := captureStdout(t, func() error { return run([]string{"--version"}) })
	if err != nil {
		t.Fatalf("run(--version): %v", err)
	}
	if !strings.HasPrefix(out, "atelier ") || strings.Contains(out, "Usage:") {
		t.Errorf("run(--version) printed %q, want a version line", out)
	}
	// The dual-flag policy keeps one spelling per action; -v is not it.
	if err := run([]string{"-v"}); err == nil {
		t.Error("run(-v) was accepted; --version is the only version spelling")
	}
}

// The shared add/apply parser must not name the `module` namespace ADR-0038
// removed: a user who mistypes a flag is told about a command that no longer
// exists.
func TestParseModuleArgs_doesNotNameTheRemovedNamespace(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"a", "b"}} {
		_, err := parseModuleArgs(args)
		if err == nil {
			t.Fatalf("parseModuleArgs(%v) accepted invalid arguments", args)
		}
		if strings.Contains(err.Error(), "module command") {
			t.Errorf("parseModuleArgs(%v) names the removed module namespace: %v", args, err)
		}
	}
}
