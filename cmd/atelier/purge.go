package main

import (
	"bufio"
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
		if a == "--force" || a == "-f" {
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
		fmt.Println("The following directories will be removed:")
		for _, d := range found {
			fmt.Printf("  %s\n", d)
		}
		fmt.Print("Continue? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("aborted")
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
