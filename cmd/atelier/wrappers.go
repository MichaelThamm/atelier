package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

const wrappersUsage = `Usage:
  atelier wrappers [PATH]
                                               List the wrappers directly under PATH (default: the
                                               current directory): each child directory holding a
                                               main.tf or .atelier/, with the modules it declares.
                                               Read-only and one level deep; no wrapper is opened
                                               or changed.
`

// runWrappers implements `atelier wrappers [PATH]`. It is a read-only view of
// the wrapper directories that share a parent (e.g. a tf-testing/ scratch
// directory); it does not address or operate on them (ADR-0036).
func runWrappers(args []string) error {
	var target string
	for _, a := range args {
		switch {
		case a == "--help" || a == "-h":
			fmt.Print(wrappersUsage)
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q for wrappers", a)
		default:
			if target != "" {
				return fmt.Errorf("wrappers accepts at most one path argument")
			}
			target = a
		}
	}

	if target == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		target = cwd
	} else {
		abs, err := filepath.Abs(target)
		if err != nil {
			return err
		}
		target = abs
	}

	found, err := findWrappers(target)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Printf("No wrappers found under %s.\n", target)
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "WRAPPER\tMODULES")
	for _, w := range found {
		mods := "-"
		if len(w.Modules) > 0 {
			mods = strings.Join(w.Modules, ", ")
		}
		fmt.Fprintf(tw, "%s\t%s\n", w.Name, mods)
	}
	return tw.Flush()
}

// childWrapper is a wrapper directory found directly under a parent.
type childWrapper struct {
	Name    string   // directory basename
	Modules []string // module block names declared in its main.tf
}

// findWrappers returns the immediate child directories of parent that look
// like wrappers, sorted by name. It does not recurse: the intended parent is a
// scratch directory holding sibling wrappers (e.g. tf-testing/), and a deeper
// search would make the boundary arbitrary and noisy (ADR-0036).
func findWrappers(parent string) ([]childWrapper, error) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, err
	}
	var out []childWrapper
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(parent, e.Name())
		if !looksLikeWrapper(dir) {
			continue
		}
		blocks, err := wrapper.ReadModuleBlocks(dir)
		if err != nil {
			return nil, err
		}
		w := childWrapper{Name: e.Name()}
		for _, b := range blocks {
			w.Modules = append(w.Modules, b.Name)
		}
		sort.Strings(w.Modules)
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// looksLikeWrapper reports whether dir is a wrapper candidate: it holds a
// main.tf or an .atelier/ directory. This is looser than isWrapperDir, which
// requires both, because a wrapper whose .atelier/ was deleted is still one
// Atelier can rehydrate.
func looksLikeWrapper(dir string) bool {
	if mainTFExists(dir) {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, wrapper.AtelierDir))
	return err == nil && info.IsDir()
}
