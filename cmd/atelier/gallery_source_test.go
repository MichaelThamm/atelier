package main

import "testing"

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
	if len(opts.VarFiles) != 1 || opts.VarFiles[0] != "haproxy-dev" {
		t.Errorf("VarFiles = %v, want [haproxy-dev]", opts.VarFiles)
	}
}

func TestResolveModuleSource_flagsWin(t *testing.T) {
	opts := moduleOpts{Source: "haproxy-product", Ref: "deadbeef", VarFiles: []string{"mine"}}
	if err := resolveModuleSource(&opts); err != nil {
		t.Fatal(err)
	}
	if opts.Ref != "deadbeef" {
		t.Errorf("an explicit --ref must win over the entry: %q", opts.Ref)
	}
	// The entry's preset is layered first so the user's bundle still wins.
	if len(opts.VarFiles) != 2 || opts.VarFiles[0] != "haproxy-dev" || opts.VarFiles[1] != "mine" {
		t.Errorf("VarFiles = %v, want [haproxy-dev mine]", opts.VarFiles)
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
