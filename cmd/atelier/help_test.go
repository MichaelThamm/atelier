package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every command answers -h and --help by printing help, and answers it the same
// way wherever the flag appears.
//
// `ls` is why this is a table. Its unknown-flag check exempted --help and -h
// without consuming them, so `atelier ls --help` printed the module table and
// exited 0 — a silent no-op, and the one help failure a script cannot detect.
// `gallery` and `presets` printed their usage for --help but exited 1, which
// fails a `atelier gallery --help` that only wants the exit code.
//
// Help is the only discovery path this CLI has for the flags a command takes,
// so it has to be reachable by the obvious guess on every one of them.
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
		{"add", runModuleAdd, usage},
		{"rm", runModuleRm, usage},
		{"ls", runModuleList, usage},
		{"apply", runModuleApply, usage},
		{"wrappers", runWrappers, wrappersUsage},
		{"purge", runPurge, usage},
		{"import", runImport, usage},
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
					// Exactly the help, so a command that fell
					// through to its body is caught: `ls` prints a
					// module table here.
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
// that `atelier wrappers --help` prints wrappers' help rather than the
// top-level usage. The per-command table above calls the run functions
// directly and cannot see this layer.
func TestRun_helpReachesTheDispatchedCommand(t *testing.T) {
	scoped := map[string]string{
		"wrappers":     wrappersUsage,
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

// The namespace and top-level help must stay reachable, and --version must not
// be swallowed by the help check that scans every argument.
func TestRun_topLevelHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		out, err := captureStdout(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if !strings.Contains(out, "atelier --help") {
			t.Errorf("run(%v) did not print the top-level usage:\n%s", args, out)
		}
	}
	for _, args := range [][]string{{"--version"}, {"-v"}} {
		out, err := captureStdout(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
		if !strings.HasPrefix(out, "atelier ") || strings.Contains(out, "Usage:") {
			t.Errorf("run(%v) printed %q, want a version line", args, out)
		}
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
