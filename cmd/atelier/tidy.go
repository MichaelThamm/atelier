package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/MichaelThamm/atelier/internal/tidy"
)

// runTidy implements `atelier tidy [PATH] [--write]`.
//
// Dry-run by default: it prints the prune diff and leaves main.tf untouched.
// With --write it backs up main.tf and applies the prune. See ADR-0021.
func runTidy(args []string) error {
	var write bool
	var target string
	for _, a := range args {
		switch {
		case a == "--write" || a == "-w":
			write = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q for tidy", a)
		default:
			if target != "" {
				return fmt.Errorf("tidy accepts at most one path argument")
			}
			target = a
		}
	}

	dir := target
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	} else {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		dir = abs
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	stop := startSpinner("Resolving module schema…")
	res, err := tidy.Run(ctx, tidy.Options{Dir: dir, Write: write})
	stop()
	if err != nil {
		return err
	}

	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	if res.AlreadyTidy {
		fmt.Fprintln(os.Stderr, "Already tidy — no defaulted values to remove.")
		return nil
	}

	// The diff goes to stdout so it can be piped/redirected; status lines go
	// to stderr.
	fmt.Print(res.Diff)

	if write {
		fmt.Fprintf(os.Stderr, "\nPruned %s. Backup: %s\n", filepath.Join(dir, "main.tf"), res.BackupPath)
	} else {
		fmt.Fprintln(os.Stderr, "\nDry run — no changes written. Re-run with --write to apply.")
	}
	return nil
}
