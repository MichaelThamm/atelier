package main

import (
	"fmt"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const presetsUsage = `Usage:
  atelier presets lint --module <dir> <file.tfvars>...
                                               Check preset .tfvars bundles against a module's declared
                                               variables. Reports names the module does not declare, keys
                                               nested in object values that the object type does not declare,
                                               and scalar type mismatches. Exits non-zero on any finding.
`

// runPresets dispatches the `atelier presets` subcommand. A preset is a
// Terraform-native `.tfvars` bundle (ADR-0031); the gallery is listed by
// `atelier gallery list`.
func runPresets(args []string) error {
	if helpRequested(args, presetsUsage) {
		return nil
	}
	if len(args) == 0 {
		fmt.Print(presetsUsage)
		return nil
	}
	switch args[0] {
	case "lint":
		return runPresetsLint(args[1:])
	default:
		return fmt.Errorf("unknown presets subcommand %q\n\n%s", args[0], presetsUsage)
	}
}

// presetsLintOpts holds parsed flags for `atelier presets lint`.
type presetsLintOpts struct {
	Module string   // --module: directory holding the module's variables.tf
	Files  []string // positional .tfvars bundles to check
}

func parsePresetsLintArgs(args []string) (presetsLintOpts, error) {
	var opts presetsLintOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--module":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("--module requires a directory")
			}
			opts.Module = args[i]
		case strings.HasPrefix(a, "--module="):
			opts.Module = strings.TrimPrefix(a, "--module=")
		case strings.HasPrefix(a, "-"):
			return opts, fmt.Errorf("unknown flag %q for presets lint", a)
		default:
			opts.Files = append(opts.Files, a)
		}
	}
	if opts.Module == "" {
		return opts, fmt.Errorf("presets lint requires --module <dir>")
	}
	if len(opts.Files) == 0 {
		return opts, fmt.Errorf("presets lint requires at least one .tfvars file")
	}
	return opts, nil
}

// runPresetsLint checks each bundle against the module schema and prints a
// per-file report. It exits non-zero when any file has a finding, so it can
// gate CI (a committed preset that names a removed field fails the build).
func runPresetsLint(args []string) error {
	if helpRequested(args, presetsUsage) {
		return nil
	}
	opts, err := parsePresetsLintArgs(args)
	if err != nil {
		return err
	}
	vars, err := tfvars.LoadDir(opts.Module)
	if err != nil {
		return err
	}

	failed := false
	for _, path := range opts.Files {
		diags, err := wrapper.LintTFVars(path, vars)
		if err != nil {
			return err
		}
		if diags.Empty() {
			fmt.Printf("OK   %s\n", path)
			continue
		}
		failed = true
		fmt.Printf("FAIL %s\n", path)
		for _, n := range diags.Unknown {
			if strings.ContainsAny(n, ".[") {
				fmt.Printf("  unknown key %q\n", n)
			} else {
				fmt.Printf("  unknown variable %q\n", n)
			}
		}
		for _, m := range diags.Mismatched {
			fmt.Printf("  %s\n", m)
		}
	}
	if failed {
		return fmt.Errorf("one or more presets did not lint cleanly")
	}
	return nil
}
