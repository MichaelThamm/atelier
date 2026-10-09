package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
	"github.com/MichaelThamm/atelier/internal/modulesource"
)

// resolveModuleSource expands a gallery entry name into the options it stands
// for (ADR-0035). A git URL or a local path is used as-is; a bare name that is
// neither resolves against the bundled gallery. Explicit flags win over the
// entry, and the entry's presets are composed ahead of any user --var-file so
// the user's own bundle still wins (ADR-0039).
func resolveModuleSource(opts *moduleOpts) error {
	return resolveSource(opts, true)
}

// resolveImportSource is resolveModuleSource for `atelier import`, which takes
// the entry's module, subdirectory and pinned ref but composes none of its
// presets: an import must describe a deployment that already exists, and an
// entry's preset describes what a good new one looks like.
func resolveImportSource(opts *moduleOpts) error {
	return resolveSource(opts, false)
}

func resolveSource(opts *moduleOpts, composePresets bool) error {
	entry, ok := galleryEntry(opts.Source)
	if !ok {
		if looksLikeSource(opts.Source) {
			return nil
		}
		return fmt.Errorf("%q is not a module URL, a local path, or a gallery entry; run 'atelier gallery list' to see the gallery", opts.Source)
	}
	opts.Source = entry.Module
	// The entry pins a revision; an explicit --ref overrides it (ADR-0055's
	// requirement for import, an opt-in for add/apply).
	pinUsed := opts.Ref == ""
	if pinUsed {
		opts.Ref = entry.Ref
	}
	if opts.ModulePath == "" {
		opts.ModulePath = entry.Subdir
	}
	if opts.As == "" {
		opts.As = entry.Block
	}

	// A gallery name is a snapshot, so always say what it resolved to. Nothing
	// else in the run shows which revision a one-liner will deploy.
	fmt.Fprintf(os.Stderr, "Gallery entry %s: %s\n", entry.Name, resolvedSourceLine(entry, opts, pinUsed))
	if !pinUsed && composePresets {
		fmt.Fprintf(os.Stderr, "  --ref overrides the entry's pin %s.\n", entry.ShortRef())
	}

	if len(entry.Presets) == 0 {
		return nil
	}
	// Name the presets either way. Composed, nothing else says which values the
	// entry chose for the user; skipped, nothing else says the entry had any.
	if composePresets {
		opts.VarFiles = append(append([]string{}, entry.Presets...), opts.VarFiles...)
		if pinUsed {
			fmt.Fprintf(os.Stderr, "  Applying its bundled presets: %s\n", strings.Join(entry.Presets, ", "))
		} else {
			// The composed presets are the entry's validated scenario at its
			// pin; the user chose another revision, so flag them for review
			// rather than applying them as if they still described it.
			fmt.Fprintf(os.Stderr,
				"  Applying its bundled presets: %s (validated against pin %s; review them for %s).\n",
				strings.Join(entry.Presets, ", "), entry.ShortRef(), opts.Ref)
		}
	} else {
		fmt.Fprintf(os.Stderr,
			"Using %s's module and revision; not applying its presets: %s\n"+
				"  An import matches what is already deployed, so supply any values that\n"+
				"  describe it yourself with --var or --var-file.\n",
			entry.Name, strings.Join(entry.Presets, ", "))
	}
	return nil
}

// resolvedSourceLine renders what a gallery name resolved to: the module, its
// subdirectory, and the revision that will be deployed. The entry's pinned SHA
// is shown short; an explicit --ref is shown as given, since a branch or track
// is already short and truncating it would hide which revision was chosen.
func resolvedSourceLine(entry gallery.Entry, opts *moduleOpts, pinUsed bool) string {
	src := opts.Source
	if opts.ModulePath != "" {
		src += "//" + opts.ModulePath
	}
	ref := opts.Ref
	if pinUsed {
		ref = entry.ShortRef()
	}
	return fmt.Sprintf("%s @%s", src, ref)
}

// looksLikeSource reports whether the positional is a git URL or a local path,
// either of which is used as-is rather than looked up in the gallery.
func looksLikeSource(src string) bool {
	return strings.Contains(src, "://") || strings.HasPrefix(src, "git@") || modulesource.IsLocal(src)
}

// galleryEntry returns the bundled entry named by src, when src is a bare name
// that is neither a URL nor a local path. It is the single lookup the source
// resolvers and `atelier import`'s guards share, so a bare name is classified
// the same way everywhere.
func galleryEntry(src string) (gallery.Entry, bool) {
	if src == "" || looksLikeSource(src) {
		return gallery.Entry{}, false
	}
	return gallery.Find(src)
}
