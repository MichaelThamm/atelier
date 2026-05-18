// Package candidate identifies module candidates within a cloned repository.
// A candidate is a directory that looks like a configurable Terraform root
// module (SPEC §5.2): contains *.tf files declaring `variable` blocks, is
// not a tests/examples directory, and is not referenced as a child module by
// another directory.
//
// Discovery is heuristic by default. An atelier.yaml at the clone root
// overrides the heuristic and declares the canonical list verbatim
// (ADR-0010).
package candidate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/manifest"
)

// Candidate is one discovered module candidate.
type Candidate struct {
	// Path is the candidate directory relative to the clone root, with
	// forward slashes (e.g. "terraform/cos-lite").
	Path string
	// Name is the display name. From the manifest if present, otherwise the
	// directory basename.
	Name string
	// Description, when non-empty, is shown next to the candidate in the
	// picker. From the manifest if present, otherwise the first paragraph of
	// README.md in the candidate directory if any.
	Description string
}

// excludedDirs are directories never considered as candidates, anywhere in
// the tree. tests/test/examples/example follow Terraform community
// convention; .atelier and .terraform are state directories belonging to
// Atelier and Terraform respectively (the latter is full of vendored module
// sources that look exactly like real candidates).
var excludedDirs = map[string]bool{
	"tests":      true,
	"test":       true,
	"examples":   true,
	"example":    true,
	".atelier":   true,
	".git":       true,
	".terraform": true,
}

// Discover walks `cloneRoot` for module candidates and applies the manifest
// override if `manifest` is non-nil.
//
// The returned slice is sorted by Path for deterministic UX. The function
// returns a non-fatal warning list alongside the result.
func Discover(cloneRoot string, m *manifest.Manifest) ([]Candidate, []string, error) {
	if m != nil && len(m.Modules) > 0 {
		// Manifest path: use the declared list verbatim. Validate that each
		// declared path actually exists in the clone; warn (not error) on
		// missing entries so a slightly-out-of-sync manifest doesn't break.
		var out []Candidate
		var warnings []string
		for _, mod := range m.Modules {
			dir := filepath.Join(cloneRoot, mod.Path)
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				warnings = append(warnings, fmt.Sprintf("manifest references missing directory %q", mod.Path))
				continue
			}
			c := Candidate{Path: filepath.ToSlash(mod.Path), Name: mod.Name, Description: mod.Description}
			if c.Description == "" {
				c.Description = readReadmeFirstPara(dir)
			}
			out = append(out, c)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		return out, warnings, nil
	}

	// Heuristic path.
	childRefs, err := collectChildModuleRefs(cloneRoot)
	if err != nil {
		return nil, nil, err
	}

	var out []Candidate
	err = filepath.WalkDir(cloneRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path == cloneRoot {
				return nil
			}
			name := filepath.Base(path)
			if excludedDirs[name] {
				return fs.SkipDir
			}
			return nil
		}
		// File node.
		if !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}
		dir := filepath.Dir(path)
		// Never exclude the clone root: if it declares variables, it's a
		// usable root module regardless of whether wrappers reference it.
		if dir != cloneRoot && childRefs[dir] {
			return nil
		}
		// Is this dir already collected?
		rel, err := filepath.Rel(cloneRoot, dir)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		for _, c := range out {
			if c.Path == relSlash {
				return nil
			}
		}
		// Does this file declare a variable?
		ok, err := fileDeclaresVariable(path)
		if err != nil {
			return err
		}
		if ok {
			out = append(out, Candidate{
				Path:        relSlash,
				Name:        filepath.Base(dir),
				Description: readReadmeFirstPara(dir),
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk clone: %w", err)
	}

	// Post-filter: remove candidates where all declared variables are type
	// `any` (or have no type constraint). Such modules are not usefully
	// editable in Atelier since every variable renders as read-only.
	var filtered []Candidate
	for _, c := range out {
		dir := filepath.Join(cloneRoot, c.Path)
		if dirHasTypedVariable(dir) {
			filtered = append(filtered, c)
		}
	}
	out = filtered

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil, nil
}

// fileDeclaresVariable parses a .tf file and returns true if it contains at
// least one `variable` block.
func fileDeclaresVariable(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL(data, path)
	if diags.HasErrors() {
		// A malformed .tf file is not Atelier's job to diagnose; just say
		// "no variable blocks here" and move on.
		return false, nil
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return false, nil
	}
	for _, block := range body.Blocks {
		if block.Type == "variable" && len(block.Labels) == 1 {
			return true, nil
		}
	}
	return false, nil
}

// dirHasTypedVariable returns true if the directory contains at least one
// variable block with a type constraint that is not `any`. A module where
// all variables are untyped or `type = any` is not usefully editable in
// Atelier (all render as read-only).
func dirHasTypedVariable(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true // err on the side of inclusion
	}
	parser := hclparse.NewParser()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		f, diags := parser.ParseHCL(data, e.Name())
		if diags.HasErrors() {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, block := range body.Blocks {
			if block.Type != "variable" || len(block.Labels) != 1 {
				continue
			}
			typeAttr, hasType := block.Body.Attributes["type"]
			if !hasType {
				// No type constraint at all — treated as `any`.
				continue
			}
			// Check if the type expression is the literal keyword `any`.
			if traversal, ok := typeAttr.Expr.(*hclsyntax.ScopeTraversalExpr); ok {
				if len(traversal.Traversal) == 1 && traversal.Traversal.RootName() == "any" {
					continue
				}
			}
			// Has a real type constraint — this module is editable.
			return true
		}
	}
	return false
}

// collectChildModuleRefs walks the clone gathering, from every `module
// "X" {}` block, the resolved absolute path of any `source = "./..."` or
// `../...` reference. These directories should not be reported as
// candidates: they're child modules, used by some other module rather than
// configurable root modules.
func collectChildModuleRefs(cloneRoot string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(cloneRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && excludedDirs[d.Name()] && path != cloneRoot {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".tf") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		parser := hclparse.NewParser()
		f, diags := parser.ParseHCL(data, path)
		if diags.HasErrors() {
			return nil
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			return nil
		}
		for _, block := range body.Blocks {
			if block.Type != "module" {
				continue
			}
			src, ok := block.Body.Attributes["source"]
			if !ok {
				continue
			}
			val, dd := src.Expr.Value(nil)
			if dd.HasErrors() || val.Type() != cty.String || val.IsNull() {
				continue
			}
			srcStr := val.AsString()
			if !strings.HasPrefix(srcStr, "./") && !strings.HasPrefix(srcStr, "../") {
				continue
			}
			abs := filepath.Clean(filepath.Join(filepath.Dir(path), srcStr))
			out[abs] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func readReadmeFirstPara(dir string) string {
	for _, candidate := range []string{"README.md", "README.MD", "README.markdown", "README"} {
		path := filepath.Join(dir, candidate)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		return firstParagraph(string(data))
	}
	return ""
}

func firstParagraph(s string) string {
	lines := strings.Split(s, "\n")
	var para []string
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		// Skip heading and metadata lines until the first paragraph.
		if len(para) == 0 && (strings.HasPrefix(trim, "#") || trim == "") {
			continue
		}
		if trim == "" {
			break
		}
		para = append(para, trim)
	}
	return strings.Join(para, " ")
}

// _ ensures the hcl package import is exercised at compile time even if no
// runtime path through this file references it directly (it does, via diag
// types, but the static analyser sometimes flags it otherwise).
var _ = hcl.Pos{}
