package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
)

const contractUsage = `Usage:
  atelier contract lint [--module <dir>] --output <name> [--require <interface>]...
                                               Check a module's integration interface: the named output
                                               must exist and be a typed object, and every --require
                                               interface must be one of its keys. Exits non-zero on any
                                               finding, so it can gate a producer's CI. --module defaults
                                               to the current directory.
`

// runContract dispatches the `atelier contract` subcommand. A module's
// integration interface is a typed, interface-keyed output (ADR-0062).
func runContract(args []string) error {
	if helpRequested(args, contractUsage) {
		return nil
	}
	if len(args) == 0 {
		fmt.Print(contractUsage)
		return nil
	}
	switch args[0] {
	case "lint":
		return runContractLint(args[1:])
	default:
		return fmt.Errorf("unknown contract subcommand %q\n\n%s", args[0], contractUsage)
	}
}

// contractLintOpts holds parsed flags for `atelier contract lint`.
type contractLintOpts struct {
	Module  string   // --module: directory holding the module's .tf files
	Output  string   // --output: the interface-catalogue output to check
	Require []string // --require: interface names the output must cover
}

func parseContractLintArgs(args []string) (contractLintOpts, error) {
	var opts contractLintOpts
	for i := 0; i < len(args); {
		if v, next, ok, err := flagValue(args, i, "--module"); ok {
			if err != nil {
				return opts, err
			}
			opts.Module = v
			i = next
			continue
		}
		if v, next, ok, err := flagValue(args, i, "--output"); ok {
			if err != nil {
				return opts, err
			}
			opts.Output = v
			i = next
			continue
		}
		if v, next, ok, err := flagValue(args, i, "--require"); ok {
			if err != nil {
				return opts, err
			}
			opts.Require = append(opts.Require, v)
			i = next
			continue
		}
		return opts, fmt.Errorf("unknown flag %q for contract lint", args[i])
	}
	if opts.Module == "" {
		opts.Module = "."
	}
	if opts.Output == "" {
		return opts, fmt.Errorf("contract lint requires --output <name>")
	}
	return opts, nil
}

// runContractLint checks that a module publishes a well-formed, interface-keyed
// catalogue: the named output exists and is a typed object, and every required
// interface is one of its keys. It exits non-zero on any finding so a producer
// can gate its own CI, and the gallery gate can call it over pinned entries.
func runContractLint(args []string) error {
	if helpRequested(args, contractUsage) {
		return nil
	}
	opts, err := parseContractLintArgs(args)
	if err != nil {
		return err
	}
	mod, err := tfvars.LoadModule(opts.Module)
	if err != nil {
		return err
	}

	var found *tfvars.Output
	for i := range mod.Outputs {
		if mod.Outputs[i].Name == opts.Output {
			found = &mod.Outputs[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("module does not expose output %q", opts.Output)
	}
	if found.Type == nil || found.Type.Kind != tftypes.KindObject {
		return fmt.Errorf("output %q is not a typed object; declare an object type so it can be matched", opts.Output)
	}

	keys := append([]string(nil), found.Type.AttrOrder...)
	sort.Strings(keys)

	var missing []string
	for _, r := range opts.Require {
		if _, ok := found.Type.Attributes[r]; !ok {
			missing = append(missing, r)
		}
	}
	sort.Strings(missing)

	fmt.Printf("interface %s: %s\n", opts.Output, strings.Join(keys, ", "))
	if len(missing) > 0 {
		for _, m := range missing {
			fmt.Printf("  missing interface %q\n", m)
		}
		return fmt.Errorf("output %q does not cover every required interface", opts.Output)
	}
	return nil
}
