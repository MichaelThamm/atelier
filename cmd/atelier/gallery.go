package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/gallery"
)

const galleryUsage = `Usage:
  atelier gallery list [--commands]
                                               List Atelier's bundled gallery of module quick starts:
                                               module, pinned ref, and the command to deploy them.
                                               --commands prints one non-applying scaffold command per
                                               entry, for scripts and CI.
  atelier gallery requires <name>
                                               Print the inputs an entry leaves to the user (a Juju model
                                               UUID, S3 credentials), one per line, as --var arguments.
                                               An entry written name=value supplies a working default.
                                               Used by the gallery check.
  atelier gallery lint
                                               Clone each entry's pinned module and check that every
                                               required input (a variable with no default) is covered by
                                               the entry's presets or requires. Exits non-zero on drift.
`

// runGallery dispatches the `atelier gallery` subcommand. The gallery is
// Atelier's curated set of module quick starts; a preset is a `.tfvars` bundle
// (ADR-0031), which a gallery entry may name — an entry composes all of them.
func runGallery(args []string) error {
	if helpRequested(args, galleryUsage) {
		return nil
	}
	if len(args) == 0 {
		fmt.Print(galleryUsage)
		return nil
	}
	switch args[0] {
	case "list", "ls":
		return runGalleryList(args[1:])
	case "requires":
		return runGalleryRequires(args[1:])
	case "lint":
		return runGalleryLint(args[1:])
	default:
		return fmt.Errorf("unknown gallery subcommand %q\n\n%s", args[0], galleryUsage)
	}
}

// runGalleryLint implements `atelier gallery lint`. For each entry it reads the
// pinned module's schema and checks that the entry covers every required input,
// so a module that gains a required variable fails here rather than at apply
// time (ADR-0039).
func runGalleryLint(args []string) error {
	if helpRequested(args, galleryUsage) {
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("gallery lint takes no arguments")
	}
	entries, err := gallery.List()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	failures := 0
	for _, e := range entries {
		required, err := bootstrap.LoadRequiredVars(ctx, e.Module, e.Ref, e.Subdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", e.Name, err)
			failures++
			continue
		}
		uncovered, stale, err := e.Coverage(required)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", e.Name, err)
			failures++
			continue
		}
		if len(uncovered) == 0 && len(stale) == 0 {
			fmt.Printf("%s  ok (%d required input(s))\n", e.Name, len(required))
			continue
		}
		failures++
		if len(uncovered) > 0 {
			fmt.Printf("%s  uncovered required input(s): %s\n", e.Name, strings.Join(uncovered, ", "))
		}
		if len(stale) > 0 {
			fmt.Printf("%s  requires not declared by the module: %s\n", e.Name, strings.Join(stale, ", "))
		}
	}
	if failures > 0 {
		return fmt.Errorf("gallery lint: %d entry/entries need attention", failures)
	}
	return nil
}

func runGalleryList(args []string) error {
	if helpRequested(args, galleryUsage) {
		return nil
	}
	commands := false
	for _, a := range args {
		switch a {
		case "--commands":
			commands = true
		default:
			return fmt.Errorf("unknown flag %q for gallery list", a)
		}
	}
	entries, err := gallery.List()
	if err != nil {
		return err
	}
	return renderGallery(os.Stdout, entries, commands)
}

// renderGallery writes the gallery listing. The default form is one entry per
// block: the name and description, the module and pinned ref it deploys, and
// the `atelier apply` command (whose required inputs, when any, wrap under it
// one flag per line so the command stays copy-pasteable). --commands writes one
// non-applying scaffold command per entry, which is what CI runs.
func renderGallery(w io.Writer, entries []gallery.Entry, commands bool) error {
	if commands {
		for _, e := range entries {
			fmt.Fprintln(w, e.ScaffoldCommand())
		}
		return nil
	}
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s  %s\n", e.Name, e.Description)
		fmt.Fprintf(w, "    %s\n", moduleRef(e))
		if len(e.Presets) > 0 {
			fmt.Fprintf(w, "    presets: %s\n", strings.Join(e.Presets, ", "))
		}
		if len(e.AvailablePresets) > 0 {
			fmt.Fprintf(w, "    optional: %s\n", strings.Join(e.AvailablePresets, ", "))
		}
		writeCommand(w, e)
	}
	return nil
}

// moduleRef renders the module and pinned ref, with the subdirectory when the
// module does not live at the repository root.
func moduleRef(e gallery.Entry) string {
	ref := e.Module
	if e.Subdir != "" {
		ref += "//" + e.Subdir
	}
	return fmt.Sprintf("%s  @%s", ref, e.ShortRef())
}

// writeCommand writes the `atelier apply <name>` command, wrapping each `--var`
// onto its own indented continuation line so a long command is readable and
// still copies as one shell line (the backslashes continue it).
func writeCommand(w io.Writer, e gallery.Entry) {
	args := e.ApplyArgs()
	head := strings.Join(args[:3], " ") // atelier apply <name>
	fmt.Fprintf(w, "    %s", head)
	rest := args[3:]
	for i := 0; i+1 < len(rest); i += 2 {
		fmt.Fprintf(w, " \\\n        %s %s", rest[i], rest[i+1])
	}
	fmt.Fprintln(w)
}

// runGalleryRequires prints, one per line, the inputs an entry leaves to the
// user (ADR-0039). An entry written `name=value` carries a working default and
// is printed verbatim, so `gallery-check` can pass it straight to `--var`. It is
// a machine-readable helper for the check, which fills the bare names with
// placeholders so it can validate an entry whose module declares
// deployment-specific required inputs.
func runGalleryRequires(args []string) error {
	if helpRequested(args, galleryUsage) {
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("gallery requires takes exactly one entry name")
	}
	entry, ok := gallery.Find(args[0])
	if !ok {
		return fmt.Errorf("no gallery entry named %q", args[0])
	}
	for _, r := range entry.Requires {
		fmt.Println(r)
	}
	return nil
}
