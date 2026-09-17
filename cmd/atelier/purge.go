package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// purgeDirs are the directories removed by `atelier purge`.
var purgeDirs = []string{".atelier", ".clone"}

// runPurge implements `atelier purge [PATH] [--force]`.
func runPurge(args []string) error {
	var force bool
	var target string

	for _, a := range args {
		if a == "--force" || a == "-f" || a == "--yes" || a == "-y" {
			force = true
		} else if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown flag %q for purge", a)
		} else {
			if target != "" {
				return fmt.Errorf("purge accepts at most one path argument")
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

	// Discover which directories exist.
	var found []string
	for _, name := range purgeDirs {
		dir := filepath.Join(target, name)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			found = append(found, dir)
		}
	}

	if len(found) == 0 {
		fmt.Println("nothing to purge")
		return nil
	}

	// Confirm unless --force.
	if !force {
		fmt.Fprintln(os.Stderr, "The following directories will be removed:")
		for _, d := range found {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
		ok, err := confirm("Continue?")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "aborted")
			return nil
		}
	}

	// Remove and report.
	for _, d := range found {
		if err := os.RemoveAll(d); err != nil {
			return fmt.Errorf("failed to remove %s: %w", d, err)
		}
		fmt.Printf("removed %s\n", d)
	}
	return nil
}
