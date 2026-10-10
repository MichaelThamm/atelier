package rootgraph

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sampleWrapper(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.tf"), `module "producer" {
  source = "./modules/producer"
  name   = "hello"
}

module "consumer" {
  source     = "./modules/consumer"
  input      = module.producer.greeting
  depends_on = [module.producer]
}
`)
	write(t, filepath.Join(dir, "modules", "producer", "variables.tf"), `variable "name" {
  type = string
}
`)
	write(t, filepath.Join(dir, "modules", "producer", "outputs.tf"), `output "greeting" {
  type  = string
  value = "hello"
}
output "endpoint" {
  value = "https://example.com"
}
`)
	write(t, filepath.Join(dir, "modules", "consumer", "variables.tf"), `variable "input" {
  type = string
}
`)
	return dir
}

func TestBuild_nodesEdgesAndOutputs(t *testing.T) {
	g, err := Build(sampleWrapper(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(g.Nodes))
	}

	producer := nodeByName(t, g, "producer")
	if !producer.OutputsKnown {
		t.Fatal("producer outputs should be known (local module)")
	}
	if len(producer.Outputs) != 2 {
		t.Fatalf("producer outputs = %d, want 2", len(producer.Outputs))
	}
	if producer.Outputs[0].Name != "greeting" || producer.Outputs[0].Type != "string" {
		t.Errorf("output[0] = %+v", producer.Outputs[0])
	}
	if producer.Outputs[1].Type != "any" {
		t.Errorf("undeclared output type = %q, want any", producer.Outputs[1].Type)
	}
	if len(producer.RequiredInputs) != 1 || producer.RequiredInputs[0] != "name" {
		t.Errorf("producer required inputs = %v", producer.RequiredInputs)
	}

	consumer := nodeByName(t, g, "consumer")
	if !consumer.OutputsKnown {
		t.Fatal("consumer outputs should be known (local module)")
	}
	if len(consumer.Outputs) != 0 {
		t.Errorf("consumer outputs = %v, want none", consumer.Outputs)
	}

	if len(g.Edges) != 2 {
		t.Fatalf("edges = %d, want 2: %+v", len(g.Edges), g.Edges)
	}
	data := g.Edges[0]
	if data.Consumer != "consumer" || data.Argument != "input" || data.Producer != "producer" ||
		data.Output != "greeting" || data.Kind != EdgeData {
		t.Errorf("data edge = %+v", data)
	}
	dep := g.Edges[1]
	if dep.Consumer != "consumer" || dep.Argument != "depends_on" || dep.Producer != "producer" ||
		dep.Output != "" || dep.Kind != EdgeDependency {
		t.Errorf("dependency edge = %+v", dep)
	}
}

func TestBuild_missingModuleIsStillANode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.tf"), `module "remote" {
  source = "git::https://github.com/o/r.git//m?ref=v1"
  input  = "x"
}
`)
	g, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(g.Nodes))
	}
	if g.Nodes[0].OutputsKnown {
		t.Error("a git block with no clone should have unknown outputs")
	}
	if len(g.Edges) != 0 {
		t.Errorf("edges = %v, want none", g.Edges)
	}
}

func TestBuild_dedupesReferencesToTheSameOutput(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.tf"), `module "p" {
  source = "./p"
}
module "c" {
  source = "./c"
  x      = { a = module.p.o.foo, b = module.p.o.bar }
}
`)
	write(t, filepath.Join(dir, "p", "outputs.tf"), "output \"o\" { value = 1 }\n")
	write(t, filepath.Join(dir, "c", "variables.tf"), "variable \"x\" {}\n")

	g, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Edges) != 1 {
		t.Fatalf("edges = %d, want 1 (both fields name p.o): %+v", len(g.Edges), g.Edges)
	}
	if g.Edges[0].Producer != "p" || g.Edges[0].Output != "o" {
		t.Errorf("edge = %+v", g.Edges[0])
	}
}

func nodeByName(t *testing.T, g *Graph, name string) Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no node %q in %+v", name, g.Nodes)
	return Node{}
}
