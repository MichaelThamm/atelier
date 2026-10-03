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
	if looksLikeSource(opts.Source) {
		return nil
	}
	entry, ok := gallery.Find(opts.Source)
	if !ok {
		return fmt.Errorf("%q is not a module URL, a local path, or a gallery entry; run 'atelier gallery list' to see the gallery", opts.Source)
	}
	opts.Source = entry.Module
	if opts.Ref == "" {
		opts.Ref = entry.Ref
	}
	if opts.ModulePath == "" {
		opts.ModulePath = entry.Subdir
	}
	if opts.As == "" {
		opts.As = entry.Block
	}
	if len(entry.Presets) > 0 {
		opts.VarFiles = append(append([]string{}, entry.Presets...), opts.VarFiles...)
		// The composed presets are otherwise invisible: nothing in the command
		// or the wrapper says which values the entry chose for you. Name them
		// where they take effect.
		fmt.Fprintf(os.Stderr, "Applying %s's bundled presets: %s\n", entry.Name, strings.Join(entry.Presets, ", "))
	}
	return nil
}

// looksLikeSource reports whether the positional is a git URL or a local path,
// either of which is used as-is rather than looked up in the gallery.
func looksLikeSource(src string) bool {
	return strings.Contains(src, "://") || strings.HasPrefix(src, "git@") || modulesource.IsLocal(src)
}
