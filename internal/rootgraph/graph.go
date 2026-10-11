// Package rootgraph models a wrapper's root as a graph: nodes are its module
// blocks, and edges are the module-output references its arguments make. It is
// a read-only derivation over main.tf and the modules it points at (ADR-0061);
// it never writes files.
package rootgraph

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// EdgeKind distinguishes a value that flows into an input from a dependency
// that only orders or repeats a module.
type EdgeKind int

const (
	// EdgeData is a module output consumed by a consumer's regular argument.
	EdgeData EdgeKind = iota
	// EdgeDependency is a reference from a module meta-argument (depends_on,
	// count, for_each): a real relationship, but not a value to wire.
	EdgeDependency
)

func (k EdgeKind) String() string {
	if k == EdgeDependency {
		return "dependency"
	}
	return "data"
}

// Edge is one argument-level reference: the consumer's argument references the
// named output (empty for a bare module reference such as `depends_on`).
type Edge struct {
	Consumer string
	Argument string
	Producer string
	Output   string
	Kind     EdgeKind
}

// Output is a producer module's declared output, as the graph exposes it. Type
// is "any" when the module declares no constraint (ADR-0060).
type Output struct {
	Name      string
	Type      string
	Sensitive bool
}

// Node is one module block.
type Node struct {
	Name   string
	Source string
	// OutputsKnown is true when the module's directory was readable and its
	// contract was parsed. A git block whose clone is absent has it false.
	OutputsKnown bool
	Outputs      []Output
	// RequiredInputs lists the module's declared variables with no default,
	// when the module directory was readable.
	RequiredInputs []string
	// Contract is the module's full declared contract (variables and outputs),
	// when the module directory was readable. Matching consumes it (ADR-0062).
	Contract *tfvars.Module
}

// Graph is a wrapper's root as read from disk.
type Graph struct {
	Dir   string
	Nodes []Node
	Edges []Edge
	// Blocks is the raw parse of main.tf's module blocks, kept so a caller can
	// inspect a specific argument's references (e.g. which interfaces an
	// existing integration wire selects).
	Blocks []wrapper.ModuleBlockArgs
}

// Build reads the wrapper at dir and derives its graph. Outputs are resolved
// from a local module directory or an existing .atelier/clone checkout; a block
// whose module cannot be found is still a node, just with no known outputs.
func Build(dir string) (*Graph, error) {
	blocks, err := wrapper.ReadModuleArgs(dir)
	if err != nil {
		return nil, err
	}
	g := &Graph{Dir: dir, Blocks: blocks}
	for _, b := range blocks {
		n := Node{Name: b.Name, Source: b.Source}
		if mdir, ok := moduleDir(dir, b.Source); ok {
			if mod, err := tfvars.LoadModule(mdir); err == nil {
				n.OutputsKnown = true
				n.Contract = &mod
				for _, o := range mod.Outputs {
					n.Outputs = append(n.Outputs, Output{
						Name:      o.Name,
						Type:      o.Type.String(),
						Sensitive: o.Sensitive,
					})
				}
				for _, v := range mod.Variables {
					if v.IsRequired() {
						n.RequiredInputs = append(n.RequiredInputs, v.Name)
					}
				}
			}
		}
		g.Nodes = append(g.Nodes, n)
	}
	seen := map[Edge]bool{}
	for _, b := range blocks {
		for _, arg := range b.Args {
			for _, ref := range arg.Refs {
				producer, output, ok := splitModuleRef(ref)
				if !ok {
					continue
				}
				e := Edge{
					Consumer: b.Name,
					Argument: arg.Name,
					Producer: producer,
					Output:   output,
					Kind:     edgeKind(arg.Name),
				}
				// One argument may reference the same output several times
				// (e.g. three fields of an object); that is one edge.
				if seen[e] {
					continue
				}
				seen[e] = true
				g.Edges = append(g.Edges, e)
			}
		}
	}
	return g, nil
}

// edgeKind classifies an argument reference. Module meta-arguments that order
// or repeat a module are dependencies, not data flow (ADR-0061).
func edgeKind(arg string) EdgeKind {
	switch arg {
	case "depends_on", "count", "for_each":
		return EdgeDependency
	default:
		return EdgeData
	}
}

// splitModuleRef reports whether ref is a module reference and, if so, the
// producer block and output it names. The output is "" for a bare module
// reference like "module.foo" (e.g. depends_on).
func splitModuleRef(ref string) (producer, output string, ok bool) {
	parts := strings.Split(ref, ".")
	if len(parts) < 2 || parts[0] != "module" || parts[1] == "" {
		return "", "", false
	}
	if len(parts) >= 3 {
		output = parts[2]
	}
	return parts[1], output, true
}

// moduleDir resolves a module block's source to a local directory: a local path
// relative to the wrapper, or an existing clone under .atelier/clone/.
func moduleDir(wrapperDir, source string) (string, bool) {
	if source == "" {
		return "", false
	}
	if modulesource.IsLocal(source) {
		p := source
		if !filepath.IsAbs(p) {
			p = filepath.Join(wrapperDir, p)
		}
		if isDir(p) {
			return p, true
		}
		return "", false
	}
	sub := modulesource.ModulePath(source)
	clone := filepath.Join(wrapperDir, ".atelier", "clone", modulesource.RepoBasename(modulesource.Remote(source)))
	if sub != "" {
		clone = filepath.Join(clone, filepath.Clean(sub))
	}
	if isDir(clone) {
		return clone, true
	}
	return "", false
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
