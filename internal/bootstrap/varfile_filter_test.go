package bootstrap

import (
	"testing"
)

// names extracts the bundle names, for comparing a filtered listing.
func names(files []VarFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

// TestModuleFilter_narrowsGalleryToTheModule is the regression test for
// `--list-var-files` on a wrapper: the gallery spans every product Atelier
// ships, so an unfiltered listing for cos-lite named ten bundles, nine of them
// inputs cos-lite does not declare.
func TestModuleFilter_narrowsGalleryToTheModule(t *testing.T) {
	got := GalleryVarFilesFor(NewModuleFilter(
		"https://github.com/canonical/observability-stack", "terraform/cos-lite"))
	if len(got) == 0 {
		t.Fatal("cos-lite has a bundled preset; filtering returned none")
	}
	for _, f := range got {
		if f.Source != "gallery" {
			t.Errorf("source = %q; want gallery", f.Source)
		}
	}
	// The unfiltered list is much larger; the point is that it shrank to the
	// entries that actually deploy this module.
	all := GalleryVarFiles()
	if len(got) >= len(all) {
		t.Errorf("filtered listing has %d bundles and unfiltered has %d; want it narrowed",
			len(got), len(all))
	}
}

// TestModuleFilter_separatesSubdirectories guards the case that motivated the
// filter: `cos` and `cos-lite` are two directories of one repository, so a
// repository-only match would offer each the other's bundles.
func TestModuleFilter_separatesSubdirectories(t *testing.T) {
	cosLite := names(GalleryVarFilesFor(NewModuleFilter(
		"https://github.com/canonical/observability-stack", "terraform/cos-lite")))
	cos := names(GalleryVarFilesFor(NewModuleFilter(
		"https://github.com/canonical/observability-stack", "terraform/cos")))

	for _, n := range cosLite {
		for _, c := range cos {
			if n == c && n == "cos-lite-no-ingress" {
				t.Error("cos-lite's bundle appeared in the cos listing")
			}
		}
	}
}

// TestModuleFilter_ignoresRefAndNormalisesRemote covers the ways a user may
// type the same repository: with a .git suffix, different case, or a ?ref that
// differs from the revision the entry pins.
func TestModuleFilter_ignoresRefAndNormalisesRemote(t *testing.T) {
	want := names(GalleryVarFilesFor(NewModuleFilter(
		"https://github.com/canonical/observability-stack", "terraform/cos-lite")))
	if len(want) == 0 {
		t.Fatal("precondition: the canonical form should match something")
	}

	for _, spelling := range []string{
		"https://github.com/canonical/observability-stack.git",
		"git::https://github.com/canonical/observability-stack.git",
		"HTTPS://GitHub.com/Canonical/observability-stack",
		"https://github.com/canonical/observability-stack?ref=deadbeef",
		"https://github.com/canonical/observability-stack/",
	} {
		got := names(GalleryVarFilesFor(NewModuleFilter(spelling, "/terraform/cos-lite/")))
		if len(got) != len(want) {
			t.Errorf("%s matched %d bundles; want %d", spelling, len(got), len(want))
		}
	}
}

// TestModuleFilter_noMatchForUnrelatedOrLocal covers the negative cases: a
// module the gallery does not ship, a nil filter meaning "everything", and a
// local path source which has no remote to match.
func TestModuleFilter_noMatchForUnrelatedOrLocal(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		subdir string
	}{
		{"unrelated module", "https://github.com/example/not-in-gallery", "terraform/x"},
		{"local path", "./cos-lite", ""},
		{"empty remote", "", "terraform/cos-lite"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := GalleryVarFilesFor(NewModuleFilter(tc.remote, tc.subdir)); len(got) != 0 {
				t.Errorf("got %v; want no bundles", names(got))
			}
		})
	}

	if got := GalleryVarFilesFor(nil); len(got) == 0 {
		t.Error("a nil filter should list every bundled bundle")
	}
}
