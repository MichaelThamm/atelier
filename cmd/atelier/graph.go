package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MichaelThamm/atelier/internal/integrate"
	"github.com/MichaelThamm/atelier/internal/rootgraph"
)

const graphUsage = `Usage:
  atelier graph [--json]
                                                 Print the wrapper's root graph: each module block,
                                                 the outputs it exposes, and the module-output
                                                 references its arguments make. Read-only; resolves
                                                 outputs from a local module directory or an
                                                 existing .atelier/clone checkout. --json writes it
                                                 to stdout.
`

// runGraph implements `atelier graph [--json]` — a read-only view of the
// wrapper's root graph (ADR-0061). It reads the current directory only and
// changes nothing.
func runGraph(args []string) error {
	if helpRequested(args, graphUsage) {
		return nil
	}
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		default:
			return fmt.Errorf("unknown flag %q for graph", a)
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	g, err := rootgraph.Build(dir)
	if err != nil {
		return err
	}

	proposals := integrate.Match(g)

	if asJSON {
		return renderJSON(os.Stdout, "graph", graphPayload(g, proposals))
	}
	printGraph(g, proposals)
	return nil
}

func printGraph(g *rootgraph.Graph, proposals []integrate.Proposal) {
	fmt.Printf("Root graph for %s\n\n", g.Dir)
	if len(g.Nodes) == 0 {
		fmt.Println("No module blocks found (is this a wrapper with a main.tf?).")
		return
	}
	for _, n := range g.Nodes {
		fmt.Println(n.Name)
		fmt.Printf("  source    %s\n", n.Source)
		if !n.OutputsKnown {
			fmt.Printf("  outputs   (module not found locally; run 'atelier' to clone it)\n\n")
			continue
		}
		if len(n.Outputs) == 0 {
			fmt.Printf("  outputs   none\n")
		}
		for _, o := range n.Outputs {
			sensitive := ""
			if o.Sensitive {
				sensitive = " (sensitive)"
			}
			fmt.Printf("  output    %s: %s%s\n", o.Name, o.Type, sensitive)
		}
		if len(n.RequiredInputs) > 0 {
			fmt.Printf("  requires  %s\n", strings.Join(n.RequiredInputs, ", "))
		}
		fmt.Println()
	}

	if len(g.Edges) == 0 {
		fmt.Println("No module-to-module wiring.")
	} else {
		fmt.Println("Wiring:")
		for _, e := range g.Edges {
			ref := "module." + e.Producer
			if e.Output != "" {
				ref += "." + e.Output
			}
			note := ""
			if e.Kind == rootgraph.EdgeDependency {
				note = " (dependency)"
			}
			fmt.Printf("  %s.%s -> %s%s\n", e.Consumer, e.Argument, ref, note)
		}
	}

	if len(proposals) > 0 {
		fmt.Println("\nProposed integrations:")
		for _, p := range proposals {
			note := ""
			if p.Existing {
				note = " (existing)"
			}
			fmt.Printf("  %s.%s <- module.%s.%s%s\n", p.Consumer, p.Input, p.Producer, p.Output, note)
			if len(p.Interfaces) > 0 {
				fmt.Printf("      interfaces: %s\n", strings.Join(p.Interfaces, ", "))
			}
		}
	}
}

// --- JSON payload ------------------------------------------------------------

type jsonGraph struct {
	Dir       string              `json:"dir"`
	Nodes     []jsonGraphNode     `json:"nodes"`
	Edges     []jsonGraphEdge     `json:"edges"`
	Proposals []jsonGraphProposal `json:"proposals"`
}

type jsonGraphProposal struct {
	Consumer   string   `json:"consumer"`
	Input      string   `json:"input"`
	Producer   string   `json:"producer"`
	Output     string   `json:"output"`
	Interfaces []string `json:"interfaces"`
	Existing   bool     `json:"existing"`
}

type jsonGraphNode struct {
	Name           string            `json:"name"`
	Source         string            `json:"source"`
	OutputsKnown   bool              `json:"outputsKnown"`
	Outputs        []jsonGraphOutput `json:"outputs"`
	RequiredInputs []string          `json:"requiredInputs"`
}

type jsonGraphOutput struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Sensitive bool   `json:"sensitive"`
}

type jsonGraphEdge struct {
	Consumer string `json:"consumer"`
	Argument string `json:"argument"`
	Producer string `json:"producer"`
	Output   string `json:"output"`
	Kind     string `json:"kind"`
}

func graphPayload(g *rootgraph.Graph, proposals []integrate.Proposal) jsonGraph {
	out := jsonGraph{
		Dir:       g.Dir,
		Nodes:     make([]jsonGraphNode, 0, len(g.Nodes)),
		Edges:     make([]jsonGraphEdge, 0, len(g.Edges)),
		Proposals: make([]jsonGraphProposal, 0, len(proposals)),
	}
	for _, n := range g.Nodes {
		outputs := make([]jsonGraphOutput, 0, len(n.Outputs))
		for _, o := range n.Outputs {
			outputs = append(outputs, jsonGraphOutput{Name: o.Name, Type: o.Type, Sensitive: o.Sensitive})
		}
		out.Nodes = append(out.Nodes, jsonGraphNode{
			Name:           n.Name,
			Source:         n.Source,
			OutputsKnown:   n.OutputsKnown,
			Outputs:        outputs,
			RequiredInputs: nonNil(n.RequiredInputs),
		})
	}
	for _, e := range g.Edges {
		out.Edges = append(out.Edges, jsonGraphEdge{
			Consumer: e.Consumer,
			Argument: e.Argument,
			Producer: e.Producer,
			Output:   e.Output,
			Kind:     e.Kind.String(),
		})
	}
	for _, p := range proposals {
		out.Proposals = append(out.Proposals, jsonGraphProposal{
			Consumer:   p.Consumer,
			Input:      p.Input,
			Producer:   p.Producer,
			Output:     p.Output,
			Interfaces: nonNil(p.Interfaces),
			Existing:   p.Existing,
		})
	}
	return out
}
