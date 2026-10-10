package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/tfvars"
)

// ParsedMain is the structured view of an existing main.tf.
type ParsedMain struct {
	ModuleBlockName string
	Source          string
	// Values: variable name → user-supplied value as a cty.Value. Variables
	// not listed in the module {} block do not appear here.
	Values map[string]cty.Value
	// UnknownAttrs holds attribute bytes Atelier didn't recognise (i.e.
	// neither `source` nor a declared variable name). Preserved verbatim.
	UnknownAttrs []RawAttr
}

// ReadMain parses main.tf in `dir` and populates a ParsedMain. The caller
// supplies the module's declared variables (so the reader can distinguish
// known-variable attributes from unknown ones).
//
// If main.tf does not exist, ReadMain returns (nil, nil) — the caller should
// treat that as a fresh wrapper.
func ReadMain(dir string, vars []tfvars.Variable) (*ParsedMain, error) {
	path := filepath.Join(dir, MainTF)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read main.tf: %w", err)
	}

	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse main.tf: %s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse main.tf: unexpected body type")
	}

	varSet := map[string]*tfvars.Variable{}
	for i := range vars {
		varSet[vars[i].Name] = &vars[i]
	}

	for _, block := range body.Blocks {
		if block.Type != "module" || len(block.Labels) != 1 {
			continue
		}
		pm := &ParsedMain{
			ModuleBlockName: block.Labels[0],
			Values:          map[string]cty.Value{},
		}
		for _, attr := range block.Body.Attributes {
			if attr.Name == "source" {
				val, dd := attr.Expr.Value(nil)
				if !dd.HasErrors() && val.Type() == cty.String && !val.IsNull() {
					pm.Source = val.AsString()
				}
				continue
			}
			if _, isVar := varSet[attr.Name]; isVar {
				val, dd := attr.Expr.Value(nil)
				if dd.HasErrors() {
					// Couldn't evaluate (e.g. references to other variables);
					// treat it as raw and skip materialising as a cty.Value.
					pm.UnknownAttrs = append(pm.UnknownAttrs, rawAttr(data, attr))
					continue
				}
				pm.Values[attr.Name] = val
				continue
			}
			// Unknown attribute — preserve.
			pm.UnknownAttrs = append(pm.UnknownAttrs, rawAttr(data, attr))
		}
		return pm, nil
	}
	return nil, fmt.Errorf("no module block in main.tf")
}

// rawAttr captures an attribute Atelier can't model as a cty.Value, storing
// both the whole `name = expr` bytes and just the expression bytes so the
// writer can re-emit the value deterministically.
func rawAttr(src []byte, attr *hclsyntax.Attribute) RawAttr {
	return RawAttr{
		Name:    attr.Name,
		Raw:     rangeBytes(src, attr.SrcRange),
		RawExpr: rangeBytes(src, attr.Expr.Range()),
	}
}

func rangeBytes(src []byte, r hcl.Range) []byte {
	if r.Start.Byte < 0 || r.End.Byte > len(src) || r.End.Byte < r.Start.Byte {
		return nil
	}
	out := make([]byte, r.End.Byte-r.Start.Byte)
	copy(out, src[r.Start.Byte:r.End.Byte])
	return out
}

// ModuleBlockInfo describes a module block found in main.tf.
type ModuleBlockInfo struct {
	Name   string // block label, e.g. "seaweedfs"
	Source string // the source attribute value
}

// ReadModuleBlocks returns info about ALL module blocks in main.tf.
// This is used by the multi-module TUI to discover secondary modules.
func ReadModuleBlocks(dir string) ([]ModuleBlockInfo, error) {
	path := filepath.Join(dir, MainTF)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read main.tf: %w", err)
	}

	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse main.tf: %s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse main.tf: unexpected body type")
	}

	var blocks []ModuleBlockInfo
	for _, block := range body.Blocks {
		if block.Type != "module" || len(block.Labels) != 1 {
			continue
		}
		info := ModuleBlockInfo{Name: block.Labels[0]}
		for _, attr := range block.Body.Attributes {
			if attr.Name == "source" {
				val, dd := attr.Expr.Value(nil)
				if !dd.HasErrors() && val.Type() == cty.String && !val.IsNull() {
					info.Source = val.AsString()
				}
				break
			}
		}
		blocks = append(blocks, info)
	}
	return blocks, nil
}

// ReadMainForBlock parses main.tf and extracts values for a specific named
// module block. Similar to ReadMain but targets a specific block by name.
func ReadMainForBlock(dir string, blockName string, vars []tfvars.Variable) (*ParsedMain, error) {
	path := filepath.Join(dir, MainTF)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read main.tf: %w", err)
	}

	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse main.tf: %s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse main.tf: unexpected body type")
	}

	varSet := map[string]*tfvars.Variable{}
	for i := range vars {
		varSet[vars[i].Name] = &vars[i]
	}

	for _, block := range body.Blocks {
		if block.Type != "module" || len(block.Labels) != 1 {
			continue
		}
		if block.Labels[0] != blockName {
			continue
		}
		pm := &ParsedMain{
			ModuleBlockName: block.Labels[0],
			Values:          map[string]cty.Value{},
		}
		for _, attr := range block.Body.Attributes {
			if attr.Name == "source" {
				val, dd := attr.Expr.Value(nil)
				if !dd.HasErrors() && val.Type() == cty.String && !val.IsNull() {
					pm.Source = val.AsString()
				}
				continue
			}
			if _, isVar := varSet[attr.Name]; isVar {
				val, dd := attr.Expr.Value(nil)
				if dd.HasErrors() {
					pm.UnknownAttrs = append(pm.UnknownAttrs, rawAttr(data, attr))
					continue
				}
				pm.Values[attr.Name] = val
				continue
			}
			pm.UnknownAttrs = append(pm.UnknownAttrs, rawAttr(data, attr))
		}
		return pm, nil
	}
	return nil, nil
}

// ModuleArg is one argument of a module block: its name, the literal source of
// its expression, and the references that expression makes (for example
// "module.producer.greeting"). Refs are de-duplicated.
type ModuleArg struct {
	Name string
	Raw  string
	Refs []string
}

// ModuleBlockArgs describes a module block: its label, its `source` attribute,
// and every other argument in declaration order.
type ModuleBlockArgs struct {
	Name   string
	Source string
	Args   []ModuleArg
}

// ReadModuleArgs parses main.tf and returns every module block with its
// arguments and resolved references. Unlike ReadMain it does not stop at the
// first block and does not need the module's variable schema; it is the read
// path the root graph consumes (ADR-0061). If main.tf does not exist it returns
// (nil, nil).
func ReadModuleArgs(dir string) ([]ModuleBlockArgs, error) {
	path := filepath.Join(dir, MainTF)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read main.tf: %w", err)
	}

	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse main.tf: %s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse main.tf: unexpected body type")
	}

	var blocks []ModuleBlockArgs
	for _, block := range body.Blocks {
		if block.Type != "module" || len(block.Labels) != 1 {
			continue
		}
		mb := ModuleBlockArgs{Name: block.Labels[0]}
		for _, attr := range attrsInSourceOrder(block.Body) {
			if attr.Name == "source" {
				if val, dd := attr.Expr.Value(nil); !dd.HasErrors() && val.Type() == cty.String && !val.IsNull() {
					mb.Source = val.AsString()
				}
				continue
			}
			mb.Args = append(mb.Args, ModuleArg{
				Name: attr.Name,
				Raw:  string(rangeBytes(data, attr.Expr.Range())),
				Refs: refsOf(attr.Expr),
			})
		}
		blocks = append(blocks, mb)
	}
	return blocks, nil
}

// attrsInSourceOrder returns a block's attributes ordered by source position.
// hclsyntax stores attributes in a map, so declaration order has to be
// recovered from their ranges.
func attrsInSourceOrder(body *hclsyntax.Body) []*hclsyntax.Attribute {
	attrs := make([]*hclsyntax.Attribute, 0, len(body.Attributes))
	for _, a := range body.Attributes {
		attrs = append(attrs, a)
	}
	sort.Slice(attrs, func(i, j int) bool {
		return attrs[i].SrcRange.Start.Byte < attrs[j].SrcRange.Start.Byte
	})
	return attrs
}

// refsOf returns the de-duplicated references an expression makes, formatted as
// dotted paths such as "module.producer.greeting".
func refsOf(expr hcl.Expression) []string {
	var refs []string
	seen := map[string]bool{}
	for _, t := range expr.Variables() {
		s := traversalString(t)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		refs = append(refs, s)
	}
	return refs
}

func traversalString(t hcl.Traversal) string {
	var b strings.Builder
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			b.WriteString(s.Name)
		case hcl.TraverseAttr:
			b.WriteString("." + s.Name)
		case hcl.TraverseIndex:
			switch {
			case s.Key.Type() == cty.String:
				b.WriteString("[" + strconv.Quote(s.Key.AsString()) + "]")
			case s.Key.Type() == cty.Number:
				b.WriteString("[" + s.Key.AsBigFloat().Text('g', -1) + "]")
			default:
				b.WriteString("[<key>]")
			}
		}
	}
	return b.String()
}
