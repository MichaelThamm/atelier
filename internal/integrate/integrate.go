// Package integrate matches a wrapper's module interfaces: it proposes a wire
// where a consumer input and a producer output name the same interface and
// their declared types are compatible. It is a read-only derivation over the
// root graph. See ADR-0062.
package integrate

import (
	"sort"

	"github.com/MichaelThamm/atelier/internal/rootgraph"
	"github.com/MichaelThamm/atelier/internal/tftypes"
)

// Proposal is a suggested module-to-module wire: assign the consumer's Input to
// module.<Producer>.<Output>.
type Proposal struct {
	Consumer string
	Input    string
	Producer string
	Output   string
	// Interfaces are the interface names the consumer's input declares and the
	// producer's output provides — the keys a user can choose from when wiring
	// a subset.
	Interfaces []string
	// Subsettable is true when the consumer's input is a map, because only a
	// map's keys are known at plan. A wired object value is unknown until the
	// producer applies, so a subset chosen from it cannot be gated on with
	// count/for_each; for an object-typed input only the whole object is wired.
	Subsettable bool
}

// Propose builds the wrapper's root graph and returns every whole-object
// interface match between two module blocks (ADR-0062). A match requires the
// same name and a compatible declared type; a module with no resolved contract,
// or an output/input with no declared type, is skipped.
func Propose(dir string) ([]Proposal, error) {
	g, err := rootgraph.Build(dir)
	if err != nil {
		return nil, err
	}
	return Match(g), nil
}

// Match returns the proposed wires over an already-built root graph.
func Match(g *rootgraph.Graph) []Proposal {
	// A wire that already exists is not a proposal.
	wired := map[[4]string]bool{}
	for _, e := range g.Edges {
		wired[[4]string{e.Consumer, e.Argument, e.Producer, e.Output}] = true
	}
	var out []Proposal
	for _, consumer := range g.Nodes {
		if consumer.Contract == nil {
			continue
		}
		for _, v := range consumer.Contract.Variables {
			// An integration interface is an object catalogue, or a map that
			// accepts one. A scalar input never matches (it would wire
			// incidental same-name values such as model_uuid).
			if !isInterfaceInput(v.Type) {
				continue
			}
			for _, producer := range g.Nodes {
				if producer.Name == consumer.Name || producer.Contract == nil {
					continue
				}
				for _, o := range producer.Contract.Outputs {
					if o.Name != v.Name || o.Type == nil || o.Type.Kind != tftypes.KindObject || !Compatible(v.Type, o.Type) {
						continue
					}
					if wired[[4]string{consumer.Name, v.Name, producer.Name, o.Name}] {
						continue
					}
					out = append(out, Proposal{
						Consumer:    consumer.Name,
						Input:       v.Name,
						Producer:    producer.Name,
						Output:      o.Name,
						Interfaces:  proposalInterfaces(v.Type, o.Type),
						Subsettable: v.Type.Kind == tftypes.KindMap,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Consumer != out[j].Consumer {
			return out[i].Consumer < out[j].Consumer
		}
		if out[i].Input != out[j].Input {
			return out[i].Input < out[j].Input
		}
		return out[i].Producer < out[j].Producer
	})
	return out
}

// isInterfaceInput reports whether an input can accept an integration
// interface: an object catalogue, or a map(string) that accepts one.
func isInterfaceInput(t *tftypes.Type) bool {
	if t == nil {
		return false
	}
	if t.Kind == tftypes.KindObject {
		return true
	}
	return t.Kind == tftypes.KindMap && t.Element != nil && t.Element.Kind == tftypes.KindString
}

// proposalInterfaces is the interface names a proposal offers: the consumer's
// own keys for an object input, or the producer's keys for a map input (a map
// declares no keys of its own).
func proposalInterfaces(consumer, producer *tftypes.Type) []string {
	var names []string
	if consumer.Kind == tftypes.KindMap {
		names = append(names, producer.AttrOrder...)
	} else {
		for _, name := range consumer.AttrOrder {
			if _, ok := producer.Attributes[name]; ok {
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// Compatible reports whether a producer output of type producer can satisfy a
// consumer input of type consumer: every attribute the consumer requires must
// be present on the producer with a compatible type. Terraform drops the
// producer's extra attributes on assignment, so a consumer may name a subset. A
// type that is undeclared on either side (KindAny) never matches, so only
// modules that publish typed interfaces are matched (ADR-0062).
func Compatible(consumer, producer *tftypes.Type) bool {
	if consumer == nil || producer == nil {
		return false
	}
	if consumer.Kind == tftypes.KindAny || producer.Kind == tftypes.KindAny {
		return false
	}
	// A map(string) consumer accepts a producer catalogue object whose values
	// are all strings. This is the subset-capable shape (ADR-0062): its keys
	// are known at plan even when the values are not.
	if consumer.Kind == tftypes.KindMap && consumer.Element != nil && consumer.Element.Kind == tftypes.KindString {
		if producer.Kind != tftypes.KindObject {
			return false
		}
		for _, name := range producer.AttrOrder {
			attr := producer.Attributes[name]
			if attr == nil || attr.Type == nil || attr.Type.Kind != tftypes.KindString {
				return false
			}
		}
		return true
	}
	if consumer.Kind != producer.Kind {
		return false
	}
	switch consumer.Kind {
	case tftypes.KindObject:
		for _, name := range consumer.AttrOrder {
			ca := consumer.Attributes[name]
			pa, ok := producer.Attributes[name]
			if !ok {
				return false
			}
			if !Compatible(ca.Type, pa.Type) {
				return false
			}
		}
		return true
	case tftypes.KindList, tftypes.KindSet, tftypes.KindMap:
		return Compatible(consumer.Element, producer.Element)
	case tftypes.KindTuple:
		if len(consumer.Tuple) != len(producer.Tuple) {
			return false
		}
		for i := range consumer.Tuple {
			if !Compatible(consumer.Tuple[i], producer.Tuple[i]) {
				return false
			}
		}
		return true
	default:
		// string / bool / number, already known to be the same kind.
		return true
	}
}
