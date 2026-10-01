package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

const galleryUsage = `Usage:
  atelier gallery list [--commands]
                                               List Atelier's bundled gallery of module quick starts:
                                               module, pinned ref, preset, and the command to deploy them.
                                               --commands prints one non-applying scaffold command per
                                               entry, for scripts and CI.
`

// runGallery dispatches the `atelier gallery` subcommand. The gallery is
// Atelier's curated set of module quick starts; a preset is a `.tfvars` bundle
// (ADR-0031), which a gallery entry may name.
func runGallery(args []string) error {
	if len(args) == 0 {
		fmt.Print(galleryUsage)
		return nil
	}
	switch args[0] {
	case "list", "ls":
		return runGalleryList(args[1:])
	default:
		return fmt.Errorf("unknown gallery subcommand %q\n\n%s", args[0], galleryUsage)
	}
}

func runGalleryList(args []string) error {
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

// renderGallery writes the gallery listing. The default form is the human quick
// start — the name and description, with the `atelier apply <name>` command
// aligned beneath; --commands writes one non-applying scaffold command per
// entry, which is what CI runs.
func renderGallery(w io.Writer, entries []gallery.Entry, commands bool) error {
	if commands {
		for _, e := range entries {
			fmt.Fprintln(w, e.ScaffoldCommand())
		}
		return nil
	}
	width := 0
	for _, e := range entries {
		if len(e.Name) > width {
			width = len(e.Name)
		}
	}
	width += 2
	indent := strings.Repeat(" ", width)
	for i, e := range entries {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%-*s%s\n", width, e.Name, e.Description)
		fmt.Fprintf(w, "%s%s\n", indent, e.ApplyCommand())
	}
	return nil
}
