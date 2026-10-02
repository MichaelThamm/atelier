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
                                               module, pinned ref, and the command to deploy them.
                                               --commands prints one non-applying scaffold command per
                                               entry, for scripts and CI.
  atelier gallery requires <name>
                                               Print the inputs an entry cannot supply (a Juju model
                                               UUID, S3 credentials), one per line. Used by the gallery
                                               check.
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
	case "requires":
		return runGalleryRequires(args[1:])
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

// runGalleryRequires prints, one per line, the inputs an entry's preset cannot
// supply. It is a machine-readable helper for `gallery-check`, which fills them
// with placeholders so it can validate an entry whose module declares
// deployment-specific required inputs.
func runGalleryRequires(args []string) error {
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
