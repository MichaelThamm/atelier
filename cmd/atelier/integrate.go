package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MichaelThamm/atelier/internal/integrate"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const integrateUsage = `Usage:
  atelier integrate [--interfaces NAME,...] [--dry-run] [--yes] [--json]
                                               Apply the wrapper's proposed integrations: for every
                                               consumer input that names a producer's interface, write
                                               input = module.<producer>.<output> into main.tf. Plain
                                               HCL, reviewable and reversible. --interfaces wires only
                                               the named interfaces (a subset); with a terminal and no
                                               flag you are prompted to choose per input. --dry-run
                                               shows them without writing; --yes skips the prompt;
                                               --json writes the result to stdout.
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
	var only []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--json":
			asJSON = true
		case a == "--interfaces":
			i++
			if i >= len(args) {
				return fmt.Errorf("--interfaces requires a comma-separated list")
			}
			only = splitList(args[i])
		case strings.HasPrefix(a, "--interfaces="):
			only = splitList(strings.TrimPrefix(a, "--interfaces="))
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

	// Decide what to wire. `chosen` is per-proposal; nil means "all interfaces".
	var chosen map[int][]string
	apply := false
	switch {
	case dryRun: // show only
	case asJSON && !yes: // machine output never prompts or writes without --yes
	case len(only) > 0:
		chosen = filterInterfaces(proposals, only)
		apply = true
	case yes:
		apply = true
	case isTerminal(os.Stdin):
		chosen, err = promptInterfaces(proposals)
		if err != nil {
			return err
		}
		apply = true
	default:
		return errNotInteractive
	}

	if apply {
		for i, p := range proposals {
			ifaces := p.Interfaces
			if chosen != nil {
				ifaces = chosen[i]
			}
			if len(ifaces) == 0 {
				continue
			}
			expr := p.WireExpr(ifaces)
			if err := wrapper.WireModuleInput(dir, p.Consumer, p.Input, expr); err != nil {
				return err
			}
			if !asJSON {
				fmt.Printf("Wired %s.%s = %s\n", p.Consumer, p.Input, expr)
			}
		}
	}

	if asJSON {
		return renderJSON(os.Stdout, "integrate", integratePayload(proposals, apply))
	}
	return nil
}

// filterInterfaces narrows each proposal to the named interfaces (keeping the
// proposal's order); a proposal left with none is omitted.
func filterInterfaces(proposals []integrate.Proposal, only []string) map[int][]string {
	want := make(map[string]bool, len(only))
	for _, name := range only {
		want[name] = true
	}
	out := map[int][]string{}
	for i, p := range proposals {
		var keep []string
		for _, name := range p.Interfaces {
			if want[name] {
				keep = append(keep, name)
			}
		}
		out[i] = keep
	}
	return out
}

// promptInterfaces asks, per proposal, which interfaces to wire. An empty
// answer means all. Answers may be numbers (as printed) or interface names.
func promptInterfaces(proposals []integrate.Proposal) (map[int][]string, error) {
	r := bufio.NewReader(os.Stdin)
	out := map[int][]string{}
	for i, p := range proposals {
		fmt.Printf("\n%s.%s <- module.%s.%s\n", p.Consumer, p.Input, p.Producer, p.Output)
		for j, name := range p.Interfaces {
			fmt.Printf("  %d) %s\n", j+1, name)
		}
		fmt.Printf("Choose interfaces (comma-separated numbers/names, empty = all): ")
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return nil, err
		}
		sel, err := parseSelection(strings.TrimSpace(line), p.Interfaces)
		if err != nil {
			return nil, err
		}
		out[i] = sel
	}
	return out, nil
}

func parseSelection(line string, interfaces []string) ([]string, error) {
	if line == "" {
		return interfaces, nil
	}
	chosen := map[string]bool{}
	for _, tok := range splitList(strings.ReplaceAll(line, " ", ",")) {
		if n, err := strconv.Atoi(tok); err == nil {
			if n < 1 || n > len(interfaces) {
				return nil, fmt.Errorf("interface number %d is out of range", n)
			}
			chosen[interfaces[n-1]] = true
			continue
		}
		found := false
		for _, name := range interfaces {
			if name == tok {
				chosen[tok] = true
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown interface %q", tok)
		}
	}
	var out []string
	for _, name := range interfaces {
		if chosen[name] {
			out = append(out, name)
		}
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func printProposals(proposals []integrate.Proposal) {
	fmt.Println("Proposed integrations:")
	for _, p := range proposals {
		fmt.Printf("  %s.%s <- module.%s.%s\n", p.Consumer, p.Input, p.Producer, p.Output)
		if len(p.Interfaces) > 0 {
			fmt.Printf("      interfaces: %s\n", strings.Join(p.Interfaces, ", "))
		}
	}
}

// --- JSON payload ------------------------------------------------------------

type jsonIntegrate struct {
	Proposals []jsonIntegrateItem `json:"proposals"`
	Applied   bool                `json:"applied"`
}

type jsonIntegrateItem struct {
	Consumer   string   `json:"consumer"`
	Input      string   `json:"input"`
	Producer   string   `json:"producer"`
	Output     string   `json:"output"`
	Interfaces []string `json:"interfaces"`
}

func integratePayload(proposals []integrate.Proposal, applied bool) jsonIntegrate {
	out := jsonIntegrate{Proposals: make([]jsonIntegrateItem, 0, len(proposals)), Applied: applied}
	for _, p := range proposals {
		out.Proposals = append(out.Proposals, jsonIntegrateItem{
			Consumer:   p.Consumer,
			Input:      p.Input,
			Producer:   p.Producer,
			Output:     p.Output,
			Interfaces: nonNil(p.Interfaces),
		})
	}
	return out
}
