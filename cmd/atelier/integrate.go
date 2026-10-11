package main

import (
	"fmt"
	"os"

	"github.com/MichaelThamm/atelier/internal/integrate"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const integrateUsage = `Usage:
  atelier integrate [--dry-run] [--yes] [--json]
                                               Apply the wrapper's proposed integrations: for every
                                               consumer input that names a producer's interface, write
                                               input = module.<producer>.<output> into main.tf. Plain
                                               HCL, reviewable and reversible. --dry-run shows them
                                               without writing; --yes skips the confirmation; --json
                                               writes the result to stdout.
`

// runIntegrate is the write side of interface matching (ADR-0062): `atelier
// graph` reports proposals, this applies them. It reclaims the name from the
// ROADMAP's parked cross-module wiring, now possible because producers publish
// typed interfaces.
func runIntegrate(args []string) error {
	if helpRequested(args, integrateUsage) {
		return nil
	}
	dryRun, yes, asJSON := false, false, false
	for _, a := range args {
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown flag %q for integrate", a)
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	proposals, err := integrate.Propose(dir)
	if err != nil {
		return err
	}

	if len(proposals) == 0 {
		if asJSON {
			return renderJSON(os.Stdout, "integrate", integratePayload(nil, false))
		}
		fmt.Println("No proposed integrations.")
		return nil
	}
	if !asJSON {
		printProposals(proposals)
	}

	// Decide whether to write. `--json` never prompts: it applies only with
	// `--yes`, so a machine consumer always gets a payload.
	apply := false
	switch {
	case dryRun: // show only
	case yes:
		apply = true
	case asJSON:
	default:
		ok, err := confirm("Write these integrations?")
		if err != nil {
			return err
		}
		apply = ok
	}

	if !apply {
		if asJSON {
			return renderJSON(os.Stdout, "integrate", integratePayload(proposals, false))
		}
		if !dryRun {
			fmt.Println("No changes.")
		}
		return nil
	}

	for _, p := range proposals {
		expr := "module." + p.Producer + "." + p.Output
		if err := wrapper.WireModuleInput(dir, p.Consumer, p.Input, expr); err != nil {
			return err
		}
		if !asJSON {
			fmt.Printf("Wired %s.%s = %s\n", p.Consumer, p.Input, expr)
		}
	}
	if asJSON {
		return renderJSON(os.Stdout, "integrate", integratePayload(proposals, true))
	}
	return nil
}

func printProposals(proposals []integrate.Proposal) {
	fmt.Println("Proposed integrations:")
	for _, p := range proposals {
		fmt.Printf("  %s.%s <- module.%s.%s\n", p.Consumer, p.Input, p.Producer, p.Output)
	}
}

// --- JSON payload ------------------------------------------------------------

type jsonIntegrate struct {
	Proposals []jsonIntegrateItem `json:"proposals"`
	Applied   bool                `json:"applied"`
}

type jsonIntegrateItem struct {
	Consumer string `json:"consumer"`
	Input    string `json:"input"`
	Producer string `json:"producer"`
	Output   string `json:"output"`
}

func integratePayload(proposals []integrate.Proposal, applied bool) jsonIntegrate {
	out := jsonIntegrate{Proposals: make([]jsonIntegrateItem, 0, len(proposals)), Applied: applied}
	for _, p := range proposals {
		out.Proposals = append(out.Proposals, jsonIntegrateItem{
			Consumer: p.Consumer,
			Input:    p.Input,
			Producer: p.Producer,
			Output:   p.Output,
		})
	}
	return out
}
