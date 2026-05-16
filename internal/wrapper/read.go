package wrapper

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/canonical/atelier/internal/tfvars"
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
					pm.UnknownAttrs = append(pm.UnknownAttrs, RawAttr{
						Name: attr.Name,
						Raw:  rangeBytes(data, attr.SrcRange),
					})
					continue
				}
				pm.Values[attr.Name] = val
				continue
			}
			// Unknown attribute — preserve.
			pm.UnknownAttrs = append(pm.UnknownAttrs, RawAttr{
				Name: attr.Name,
				Raw:  rangeBytes(data, attr.SrcRange),
			})
		}
		return pm, nil
	}
	return nil, fmt.Errorf("no module block in main.tf")
}

func rangeBytes(src []byte, r hcl.Range) []byte {
	if r.Start.Byte < 0 || r.End.Byte > len(src) {
		return nil
	}
	out := make([]byte, r.End.Byte-r.Start.Byte)
	copy(out, src[r.Start.Byte:r.End.Byte])
	return out
}

// ReadSecrets parses secrets.auto.tfvars and returns a map of
// variable-name to cty.Value. Returns an empty map if the file doesn't exist
// (no secrets configured yet).
func ReadSecrets(dir string) (map[string]cty.Value, error) {
	path := filepath.Join(dir, SecretsAuto)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]cty.Value{}, nil
		}
		return nil, fmt.Errorf("read secrets.auto.tfvars: %w", err)
	}
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse secrets.auto.tfvars: %s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("parse secrets.auto.tfvars: unexpected body type")
	}
	out := map[string]cty.Value{}
	for name, attr := range body.Attributes {
		val, dd := attr.Expr.Value(nil)
		if dd.HasErrors() {
			continue
		}
		out[name] = val
	}
	return out, nil
}
