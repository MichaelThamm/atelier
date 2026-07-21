package main

import "testing"

// --- decomposeModuleSource ---

func TestDecomposeModuleSource_HTTPS(t *testing.T) {
	url, ref := decomposeModuleSource("https://github.com/org/repo.git")
	if url != "https://github.com/org/repo.git" {
		t.Errorf("url: got %q", url)
	}
	if ref != "" {
		t.Errorf("ref: got %q, want empty", ref)
	}
}

func TestDecomposeModuleSource_WithRef(t *testing.T) {
	url, ref := decomposeModuleSource("https://github.com/org/repo.git?ref=v1.2.0")
	if url != "https://github.com/org/repo.git" {
		t.Errorf("url: got %q", url)
	}
	if ref != "v1.2.0" {
		t.Errorf("ref: got %q, want v1.2.0", ref)
	}
}

func TestDecomposeModuleSource_GitPrefix(t *testing.T) {
	url, ref := decomposeModuleSource("git::https://github.com/org/repo.git?ref=main")
	if url != "https://github.com/org/repo.git" {
		t.Errorf("url: got %q", url)
	}
	if ref != "main" {
		t.Errorf("ref: got %q, want main", ref)
	}
}

func TestDecomposeModuleSource_SubPath(t *testing.T) {
	url, ref := decomposeModuleSource("https://github.com/org/repo.git//modules/cos-lite?ref=v1")
	if url != "https://github.com/org/repo.git" {
		t.Errorf("url: got %q", url)
	}
	if ref != "v1" {
		t.Errorf("ref: got %q, want v1", ref)
	}
}

func TestDecomposeModuleSource_GitPrefixWithSubPath(t *testing.T) {
	url, ref := decomposeModuleSource("git::https://github.com/org/repo.git//subdir?ref=abc123")
	if url != "https://github.com/org/repo.git" {
		t.Errorf("url: got %q", url)
	}
	if ref != "abc123" {
		t.Errorf("ref: got %q, want abc123", ref)
	}
}

func TestDecomposeModuleSource_NoScheme(t *testing.T) {
	url, ref := decomposeModuleSource("github.com/org/repo.git")
	if url != "github.com/org/repo.git" {
		t.Errorf("url: got %q", url)
	}
	if ref != "" {
		t.Errorf("ref: got %q, want empty", ref)
	}
}

// --- modulePathFromSource ---

func TestModulePathFromSource_NoSubPath(t *testing.T) {
	got := modulePathFromSource("https://github.com/org/repo.git")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestModulePathFromSource_WithSubPath(t *testing.T) {
	got := modulePathFromSource("https://github.com/org/repo.git//modules/cos-lite?ref=v1")
	if got != "modules/cos-lite" {
		t.Errorf("got %q, want modules/cos-lite", got)
	}
}

func TestModulePathFromSource_GitPrefix(t *testing.T) {
	got := modulePathFromSource("git::https://github.com/org/repo.git//subdir")
	if got != "subdir" {
		t.Errorf("got %q, want subdir", got)
	}
}

func TestModulePathFromSource_SubPathOnly(t *testing.T) {
	got := modulePathFromSource("https://github.com/org/repo.git//a/b/c")
	if got != "a/b/c" {
		t.Errorf("got %q, want a/b/c", got)
	}
}

// --- isLocalPath ---

func TestIsLocalPath_Absolute(t *testing.T) {
	if !isLocalPath("/home/user/module") {
		t.Error("expected true for absolute path")
	}
}

func TestIsLocalPath_Relative(t *testing.T) {
	if !isLocalPath("./module") {
		t.Error("expected true for ./path")
	}
}

func TestIsLocalPath_ParentRelative(t *testing.T) {
	if !isLocalPath("../module") {
		t.Error("expected true for ../path")
	}
}

func TestIsLocalPath_GitURL(t *testing.T) {
	if isLocalPath("https://github.com/org/repo.git") {
		t.Error("expected false for git URL")
	}
}

func TestIsLocalPath_GitSSH(t *testing.T) {
	if isLocalPath("git@github.com:org/repo.git") {
		t.Error("expected false for SSH URL")
	}
}
