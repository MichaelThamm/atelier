package wrapper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

const OutputsTF = "outputs.tf"

// DiscoverOutputNames scans every *.tf file in dir and returns the names of
// all `output` blocks found, sorted alphabetically.
func DiscoverOutputNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read module dir %s: %w", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, "_test.tf") {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)

	parser := hclparse.NewParser()
	var names []string
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		f, diags := parser.ParseHCL(data, path)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse %s: %s", path, diags.Error())
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("parse %s: unexpected body type", path)
		}
		for _, block := range body.Blocks {
			if block.Type == "output" && len(block.Labels) == 1 {
				names = append(names, block.Labels[0])
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// bootstrapOutputs writes outputs.tf into the wrapper directory. Each output
// forwards the corresponding module output:
//
//	output "name" {
//	  value = module.<moduleBlockName>.<name>
//	}
func bootstrapOutputs(opts BootstrapOptions) error {
	path := filepath.Join(opts.Dir, OutputsTF)
	if _, err := os.Stat(path); err == nil {
		return nil // don't overwrite existing
	}
	if len(opts.OutputNames) == 0 {
		return nil // nothing to forward
	}

	file := hclwrite.NewEmptyFile()
	for i, name := range opts.OutputNames {
		if i > 0 {
			file.Body().AppendNewline()
		}
		block := file.Body().AppendNewBlock("output", []string{name})
		body := block.Body()
		body.SetAttributeTraversal("value", hcl.Traversal{
			hcl.TraverseRoot{Name: "module"},
			hcl.TraverseAttr{Name: opts.ModuleBlockName},
			hcl.TraverseAttr{Name: name},
		})
	}
	return os.WriteFile(path, hclwrite.Format(file.Bytes()), 0o644)
}

// EnsureOutputs writes outputs.tf if it does not already exist. Called from
// LoadExisting so that wrappers bootstrapped before output forwarding was
// added get the file on next open.
func EnsureOutputs(dir, moduleBlockName string, outputNames []string) error {
	return bootstrapOutputs(BootstrapOptions{
		Dir:             dir,
		ModuleBlockName: moduleBlockName,
		OutputNames:     outputNames,
	})
}
