// Command gallerysite renders Atelier's bundled module gallery as the Markdown
// page the GitHub Pages site publishes. It reads the same embedded manifest the
// CLI reads and reuses gallery.Entry's derivation, so the showcase cannot drift
// from `atelier gallery list`. See ADR-0037.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

func main() {
	out := flag.String("o", "", "write the page to this file instead of stdout")
	optionalVars := flag.String("optional-vars", "", "print the Juju model pin each entry passes beyond its manifest requires, one `name var` per line")
	flag.Parse()

	entries, err := gallery.List()
	if err != nil {
		fatal(err)
	}

	if *optionalVars != "" {
		printOptionalVars(entries)
		return
	}

	if *out == "" {
		if err := render(os.Stdout, entries); err != nil {
			fatal(err)
		}
		return
	}
	f, err := os.Create(*out)
	if err != nil {
		fatal(err)
	}
	if err := render(f, entries); err != nil {
		f.Close()
		fatal(err)
	}
	if err := f.Close(); err != nil {
		fatal(err)
	}
}

// printOptionalVars reports the model pins the Juju variants invent, for
// `just gallery-check` to assert against each module.
func printOptionalVars(entries []gallery.Entry) {
	for _, e := range entries {
		for _, v := range jujuOptionalVars(e) {
			fmt.Println(e.Name + " " + v)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gallerysite:", err)
	os.Exit(1)
}

// render writes the gallery page: a short intro, the Juju conventions in a
// collapsed block, and one Material grid card per entry. A Juju entry's card
// carries its collapsed variant beneath the neutral command. The cards are
// Markdown inside a Material `.grid.cards` div, which the site enables with
// attr_list and md_in_html.
func render(w io.Writer, entries []gallery.Entry) error {
	var b strings.Builder
	b.WriteString("# Module gallery\n\n")
	b.WriteString("Atelier ships a curated quick start for each module below: a pinned revision,\n")
	b.WriteString("and a preset bundle where the module needs one. Apply an entry by name and\n")
	b.WriteString("Atelier expands it to the module, ref, and preset — the command below is the\n")
	b.WriteString("same one `atelier gallery list` prints.\n\n")
	b.WriteString("Entries that deploy with Juju also offer a collapsed variant that targets the\n")
	b.WriteString("model you have switched to.\n\n")
	writeJujuBanner(&b, entries)
	b.WriteString("<div class=\"grid cards\" markdown>\n\n")
	for _, e := range entries {
		writeCard(&b, e)
	}
	b.WriteString("</div>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// writeCard writes one entry as a Material grid card: the description, the
// module link and pinned ref, the presets, and the apply one-liner. The
// one-liner already carries every required input as a `--var`, so the card does
// not restate them. A Juju entry then carries its collapsed variant, or the
// reason it has none. The four-space indent keeps every line inside the list
// item that forms the card.
func writeCard(b *strings.Builder, e gallery.Entry) {
	fmt.Fprintf(b, "-   __%s__\n\n", e.Name)
	b.WriteString("    ---\n\n")
	fmt.Fprintf(b, "    %s\n\n", e.Description)

	fmt.Fprintf(b, "    [:octicons-mark-github-16: %s](%s)\n\n", repoLabel(e.Module), e.Module)

	meta := make([]string, 0, 5)
	if e.Subdir != "" {
		meta = append(meta, "`"+e.Subdir+"`")
	}
	meta = append(meta, "pinned `"+e.ShortRef()+"`")
	if len(e.Presets) > 0 {
		meta = append(meta, "presets "+codeList(e.Presets))
	}
	if e.Block != "" {
		meta = append(meta, "block `"+e.Block+"`")
	}
	fmt.Fprintf(b, "    %s\n\n", strings.Join(meta, " · "))

	if len(e.AvailablePresets) > 0 {
		fmt.Fprintf(b, "    Also available: %s — add one with `--var-file <name>`.\n\n",
			codeList(e.AvailablePresets))
	}

	fmt.Fprintf(b, "    ```bash\n    %s\n    ```\n\n", e.ApplyCommand())

	if createsOwnModel(e) {
		fmt.Fprintf(b, "    No variant: %s.\n\n", jujuOwnModel[e.Name])
		return
	}
	writeJujuVariant(b, e)
}

// repoLabel is the short owner/name form of a module URL, used as the card's
// link text. A non-GitHub host keeps its host and path.
func repoLabel(module string) string {
	s := module
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	return strings.TrimSuffix(s, ".git")
}
