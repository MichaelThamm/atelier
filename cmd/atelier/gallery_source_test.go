package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

func TestResolveModuleSource_galleryName(t *testing.T) {
	// Compare against the manifest, not a literal SHA: the pin moves on its own
	// cadence (ADR-0040), so a literal would fail every scheduled bump.
	entry, ok := gallery.Find("haproxy-product")
	if !ok {
		t.Fatal("no gallery entry named haproxy-product")
	}
	opts := moduleOpts{Source: "haproxy-product"}
	if err := resolveModuleSource(&opts); err != nil {
		t.Fatalf("resolveModuleSource: %v", err)
	}
	if opts.Source != "https://github.com/canonical/haproxy-operator" {
		t.Errorf("Source = %q", opts.Source)
	}
	if opts.ModulePath != "terraform/product" {
		t.Errorf("ModulePath = %q", opts.ModulePath)
	}
	if opts.As != "haproxy" {
		t.Errorf("As = %q", opts.As)
	}
	if opts.Ref != entry.Ref {
		t.Errorf("Ref = %q, want the entry's %q", opts.Ref, entry.Ref)
	}
	// The entry names no preset, so nothing is layered under a --var-file.
	if len(opts.VarFiles) != 0 {
		t.Errorf("VarFiles = %v, want none", opts.VarFiles)
	}
}

func TestResolveModuleSource_composesPresets(t *testing.T) {
	// An entry composes all of its presets, in order (later wins), ahead of any
	// user-supplied bundle.
	opts := moduleOpts{Source: "cos"}
	if err := resolveModuleSource(&opts); err != nil {
		t.Fatalf("resolveModuleSource: %v", err)
	}
	if len(opts.VarFiles) != 1 || opts.VarFiles[0] != "cos-grafana-single-unit" {
		t.Errorf("VarFiles = %v, want [cos-grafana-single-unit]", opts.VarFiles)
	}
}

// An entry's composed presets are named on stderr, since nothing in the command
// or the wrapper says which values the entry chose for the user.
func TestResolveModuleSource_namesComposedPresets(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	opts := moduleOpts{Source: "cos"}
	resolveErr := resolveModuleSource(&opts)
	w.Close()
	os.Stderr = old
	got, _ := io.ReadAll(r)
	if resolveErr != nil {
		t.Fatalf("resolveModuleSource: %v", resolveErr)
	}
	if !strings.Contains(string(got), "cos-grafana-single-unit") {
		t.Errorf("stderr = %q, want the composed preset named", got)
	}
}

// An entry's available presets are offered, not composed: they must stay out of
// the composed set so the entry deploys at the module's own defaults unless the
// user asks for one by name.
func TestResolveModuleSource_leavesAvailablePresetsAlone(t *testing.T) {
	entry, ok := gallery.Find("cos")
	if !ok {
		t.Fatal("no gallery entry named cos")
	}
	if len(entry.AvailablePresets) == 0 {
		t.Fatal("cos declares no available presets to test")
	}
	opts := moduleOpts{Source: "cos"}
	if err := resolveModuleSource(&opts); err != nil {
		t.Fatal(err)
	}
	composed := map[string]bool{}
	for _, v := range opts.VarFiles {
		composed[v] = true
	}
	for _, p := range entry.AvailablePresets {
		if composed[p] {
			t.Errorf("available preset %q must not be composed", p)
		}
	}
}

func TestResolveModuleSource_flagsWin(t *testing.T) {
	opts := moduleOpts{Source: "cos", Ref: "deadbeef", VarFiles: []string{"mine"}}
	if err := resolveModuleSource(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.Ref != "deadbeef" {
		t.Errorf("an explicit --ref must win over the entry: %q", opts.Ref)
	}
	// The entry's presets are layered first so the user's bundle still wins.
	want := []string{"cos-grafana-single-unit", "mine"}
	if len(opts.VarFiles) != len(want) {
		t.Fatalf("VarFiles = %v, want %v", opts.VarFiles, want)
	}
	for i := range want {
		if opts.VarFiles[i] != want[i] {
			t.Errorf("VarFiles[%d] = %q, want %q", i, opts.VarFiles[i], want[i])
		}
	}
}

func TestResolveModuleSource_urlAndPathUntouched(t *testing.T) {
	for _, src := range []string{
		"https://github.com/x/y",
		"git@github.com:x/y.git",
		"./local",
		"../local",
		"/abs/local",
	} {
		opts := moduleOpts{Source: src}
		if err := resolveModuleSource(&opts); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if opts.Source != src {
			t.Errorf("%s: Source changed to %q", src, opts.Source)
		}
	}
}

func TestResolveModuleSource_unknownName(t *testing.T) {
	opts := moduleOpts{Source: "no-such-entry"}
	if err := resolveModuleSource(&opts); err == nil {
		t.Error("an unknown bare name must error")
	}
}

// --- resolveImportSource: the same expansion, without the presets ---

func TestResolveImportSource_expandsEntry(t *testing.T) {
	// As with resolveModuleSource, the expected ref comes from the manifest so a
	// scheduled ref bump does not fail this test (ADR-0040).
	entry, ok := gallery.Find("cos-lite")
	if !ok {
		t.Fatal("no gallery entry named cos-lite")
	}
	opts := moduleOpts{Source: "cos-lite"}
	if err := resolveImportSource(&opts); err != nil {
		t.Fatalf("resolveImportSource: %v", err)
	}
	if opts.Source != "https://github.com/canonical/observability-stack" {
		t.Errorf("Source = %q", opts.Source)
	}
	if opts.ModulePath != "terraform/cos-lite" {
		t.Errorf("ModulePath = %q", opts.ModulePath)
	}
	if opts.Ref != entry.Ref {
		t.Errorf("Ref = %q, want the entry's %q", opts.Ref, entry.Ref)
	}
}

// An import must describe a deployment that already exists, so the entry's
// composed presets — which describe what a good new one looks like — are not
// applied. An entry that composes some (`cos`) is the case that matters.
func TestResolveImportSource_composesNoPresets(t *testing.T) {
	opts := moduleOpts{Source: "cos", VarFiles: []string{"mine"}}
	if err := resolveImportSource(&opts); err != nil {
		t.Fatalf("resolveImportSource: %v", err)
	}
	if len(opts.VarFiles) != 1 || opts.VarFiles[0] != "mine" {
		t.Errorf("VarFiles = %v, want only the user's own [mine]", opts.VarFiles)
	}
}

// The skipped presets are named, since nothing else says the entry had any.
func TestResolveImportSource_namesSkippedPresets(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	opts := moduleOpts{Source: "cos"}
	resolveErr := resolveImportSource(&opts)
	w.Close()
	os.Stderr = old
	got, _ := io.ReadAll(r)
	if resolveErr != nil {
		t.Fatalf("resolveImportSource: %v", resolveErr)
	}
	if !strings.Contains(string(got), "cos-grafana-single-unit") {
		t.Errorf("stderr = %q, want the skipped preset named", got)
	}
}

func TestResolveImportSource_flagsWin(t *testing.T) {
	opts := moduleOpts{Source: "cos-lite", Ref: "track/3.0"}
	if err := resolveImportSource(&opts); err != nil {
		t.Fatalf("resolveImportSource: %v", err)
	}
	if opts.Ref != "track/3.0" {
		t.Errorf("an explicit --ref must win over the entry's pin: %q", opts.Ref)
	}
}

func TestResolveImportSource_urlUntouched(t *testing.T) {
	const url = "https://github.com/canonical/observability-stack.git"
	opts := moduleOpts{Source: url}
	if err := resolveImportSource(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.Source != url {
		t.Errorf("Source = %q, want it unchanged", opts.Source)
	}
}

func TestResolveImportSource_unknownName(t *testing.T) {
	opts := moduleOpts{Source: "no-such-entry"}
	err := resolveImportSource(&opts)
	if err == nil {
		t.Fatal("an unknown bare name must error")
	}
	// The failure names the gallery rather than letting git report it.
	if !strings.Contains(err.Error(), "gallery list") {
		t.Errorf("error = %q, want it to point at the gallery", err)
	}
}

// --- galleryEntry ---

// A gallery name resolves; a URL, a path, an unknown name or an empty string
// does not, which is what lets the positional guard stay quiet for providers.
func TestGalleryEntry_recognisesNamesOnly(t *testing.T) {
	if e, ok := galleryEntry("cos-lite"); !ok || e.Name != "cos-lite" {
		t.Errorf("galleryEntry(cos-lite) = %+v, %v", e, ok)
	}
	for _, src := range []string{
		"https://github.com/x/y", "git@github.com:x/y.git",
		"./local", "../local", "/abs/local", "no-such-entry", "",
	} {
		if _, ok := galleryEntry(src); ok {
			t.Errorf("galleryEntry(%q) matched; want not a gallery entry", src)
		}
	}
}

// --- refuseSourcePositional: import's positional is the provider ---

func TestRefuseSourcePositional_galleryEntry(t *testing.T) {
	err := refuseSourcePositional("cos-lite")
	if err == nil {
		t.Fatal("a gallery name as the positional must be refused")
	}
	// It must name the entry, the flag that replaces it, and the revision flag.
	for _, want := range []string{"cos-lite", "--source", "--ref"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestRefuseSourcePositional_sourceLookingValue(t *testing.T) {
	for _, src := range []string{"https://github.com/x/y", "git@github.com:x/y.git", "./mod", "/abs/mod"} {
		err := refuseSourcePositional(src)
		if err == nil {
			t.Fatalf("%q must be refused as a positional", src)
		}
		if !strings.Contains(err.Error(), "--source") {
			t.Errorf("error = %q, want it to point at --source", err)
		}
	}
}

func TestRefuseSourcePositional_providerIsFine(t *testing.T) {
	for _, arg := range []string{"", "juju", "juju/juju", "hashicorp/aws", "registry.terraform.io/juju/juju"} {
		if err := refuseSourcePositional(arg); err != nil {
			t.Errorf("refuseSourcePositional(%q) = %v, want nil", arg, err)
		}
	}
}

// --- requireGalleryRef: a gallery source names its own revision ---

func TestRequireGalleryRef_requiresRefForGallery(t *testing.T) {
	err := requireGalleryRef("cos-lite", "", false)
	if err == nil {
		t.Fatal("a gallery source without --ref must be refused")
	}
	if !strings.Contains(err.Error(), "cos-lite") || !strings.Contains(err.Error(), "--ref") {
		t.Errorf("error = %q, want the entry and --ref", err)
	}
}

func TestRequireGalleryRef_explicitRefSatisfies(t *testing.T) {
	if err := requireGalleryRef("cos-lite", "track/3.0", false); err != nil {
		t.Errorf("an explicit --ref must satisfy the requirement: %v", err)
	}
}

func TestRequireGalleryRef_ignoresNonGallerySources(t *testing.T) {
	for _, src := range []string{"", "https://github.com/x/y", "./mod"} {
		if err := requireGalleryRef(src, "", false); err != nil {
			t.Errorf("requireGalleryRef(%q) = %v, want nil", src, err)
		}
	}
}

// No matching happens when listing bundles, so there is no revision to get right.
func TestRequireGalleryRef_listVarFilesNeedsNoRef(t *testing.T) {
	if err := requireGalleryRef("cos-lite", "", true); err != nil {
		t.Errorf("listing var files must not require --ref: %v", err)
	}
}
