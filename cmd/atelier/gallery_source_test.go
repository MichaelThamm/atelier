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
