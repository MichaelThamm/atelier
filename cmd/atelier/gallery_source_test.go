package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

func TestResolveModuleSource_galleryName(t *testing.T) {
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
	if opts.Ref != "a4b85299f23740e8b7570b05a0d7d4aabcd3c476" {
		t.Errorf("Ref = %q", opts.Ref)
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
	if opts.Ref != "d1598ff3bdf9a25af69145fd557a913e2a13a314" {
		t.Errorf("Ref = %q", opts.Ref)
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
