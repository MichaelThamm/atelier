package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

const committedManifest = "../../internal/gallery/gallery.json"

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// A scheduled bump must not reformat the manifest. Pin that here: patching the
// committed file with the refs it already has must be byte-identical, so a run
// that finds nothing to do produces no diff at all.
func TestRewriteRefs_noOpIsByteIdentical(t *testing.T) {
	src := readFile(t, committedManifest)
	entries, err := readManifest(src)
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}
	got, err := rewriteRefs(src, entries)
	if err != nil {
		t.Fatalf("rewriteRefs: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Errorf("a no-op rewrite changed the manifest\n--- got ---\n%s\n--- want ---\n%s", got, src)
	}
}

// Only the ref lines may change; the rest of the file is the maintainers' text.
func TestRewriteRefs_patchesOnlyTheRefLine(t *testing.T) {
	before := `[
  {
    "name": "a",
    "description": "A: a module with a hand-written description.",
    "module": "https://example.com/a",
    "subdir": "terraform/a",
    "ref": "1111111111111111111111111111111111111111",
    "presets": ["one", "two"],
    "requires": ["model"]
  }
]`
	path := writeManifest(t, before)
	src := readFile(t, path)
	entries, err := readManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Ref = "9999999999999999999999999999999999999999"

	got, err := rewriteRefs(src, entries)
	if err != nil {
		t.Fatalf("rewriteRefs: %v", err)
	}
	want := strings.Replace(before, "1111111111111111111111111111111111111111", "9999999999999999999999999999999999999999", 1)
	if string(got) != want {
		t.Errorf("only the ref should change.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRewriteRefs_errorsWhenARefIsMissing(t *testing.T) {
	// One entry, no ref line: patching would leave the manifest inconsistent,
	// so it must fail rather than write a half-patched file.
	src := []byte("[\n  {\n    \"name\": \"a\",\n    \"module\": \"https://example.com/a\"\n  }\n]\n")
	entries, err := readManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rewriteRefs(src, entries); err == nil {
		t.Error("expected an error when a ref line is missing")
	}
}

func TestRun_bumpsAndWrites(t *testing.T) {
	path := writeManifest(t, `[
  {
    "name": "a",
    "description": "A.",
    "module": "https://example.com/a",
    "ref": "1111111111111111111111111111111111111111"
  },
  {
    "name": "b",
    "description": "B.",
    "module": "https://example.com/b",
    "ref": "2222222222222222222222222222222222222222"
  }
]`)

	resolve := func(module string) (string, error) {
		switch module {
		case "https://example.com/a":
			return "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil
		default:
			return "2222222222222222222222222222222222222222", nil
		}
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-manifest", path}, &stdout, &stderr, resolve); code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr.String())
	}

	got := string(readFile(t, path))
	if !strings.Contains(got, `"ref": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`) {
		t.Errorf("entry a was not bumped:\n%s", got)
	}
	if !strings.Contains(got, `"ref": "2222222222222222222222222222222222222222"`) {
		t.Errorf("entry b must keep its pin:\n%s", got)
	}
	if !strings.Contains(stdout.String(), "a: 111111111111 -> aaaaaaaaaaaa") {
		t.Errorf("stdout missing the bump line:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "b:") {
		t.Errorf("an unchanged entry should not be reported:\n%s", stdout.String())
	}
}

func TestRun_nothingToBump(t *testing.T) {
	path := writeManifest(t, `[
  {
    "name": "a",
    "description": "A.",
    "module": "https://example.com/a",
    "ref": "1111111111111111111111111111111111111111"
  }
]`)
	before := readFile(t, path)
	resolve := func(string) (string, error) { return "1111111111111111111111111111111111111111", nil }

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-manifest", path}, &stdout, &stderr, resolve); code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no bumps") {
		t.Errorf("stdout should report no bumps:\n%s", stdout.String())
	}
	if !bytes.Equal(readFile(t, path), before) {
		t.Error("a run with no bumps must not touch the manifest")
	}
}

func TestRun_dryRunLeavesManifestUntouched(t *testing.T) {
	original := `[
  {
    "name": "a",
    "description": "A.",
    "module": "https://example.com/a",
    "ref": "1111111111111111111111111111111111111111"
  }
]`
	path := writeManifest(t, original)
	resolve := func(string) (string, error) { return "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", nil }

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-manifest", path, "-dry-run"}, &stdout, &stderr, resolve); code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, stderr.String())
	}
	if got := string(readFile(t, path)); got != original {
		t.Errorf("dry run rewrote the manifest:\n%s", got)
	}
	if !strings.Contains(stdout.String(), "dry run") {
		t.Errorf("stdout should say it was a dry run:\n%s", stdout.String())
	}
}

// An unreachable module must fail the run and leave that pin alone: a
// half-bumped manifest is worse than a loud failure.
func TestRun_unreachableModuleFails(t *testing.T) {
	path := writeManifest(t, `[
  {
    "name": "a",
    "description": "A.",
    "module": "https://example.com/a",
    "ref": "1111111111111111111111111111111111111111"
  }
]`)
	before := readFile(t, path)
	resolve := func(string) (string, error) { return "", errUnreachable }

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-manifest", path}, &stdout, &stderr, resolve); code != 1 {
		t.Errorf("exit = %d, want 1 so the scheduled run reports the failure", code)
	}
	if !bytes.Equal(readFile(t, path), before) {
		t.Error("a failed run must not write the manifest")
	}
}

// Two entries can share a module (charmarr and charmarr-plus); each ref line
// must be patched from its own entry, not from the last one seen.
func TestBump_entriesSharingAModule(t *testing.T) {
	entries := []gallery.Entry{
		{Name: "charmarr", Module: "https://example.com/charmarr", Ref: "old-a"},
		{Name: "charmarr-plus", Module: "https://example.com/charmarr", Ref: "old-b"},
	}
	resolve := func(string) (string, error) { return "shared", nil }
	changes, failures := bump(entries, resolve)
	if failures != 0 {
		t.Fatalf("failures = %d", failures)
	}
	if len(changes) != 2 {
		t.Errorf("changes = %v, want one per entry", changes)
	}
	if entries[0].Ref != "shared" || entries[1].Ref != "shared" {
		t.Errorf("both entries should be bumped, got %q and %q", entries[0].Ref, entries[1].Ref)
	}
}

func TestBump_emptySHAIsAFailure(t *testing.T) {
	entries := []gallery.Entry{{Name: "a", Module: "https://example.com/a", Ref: "old"}}
	changes, failures := bump(entries, func(string) (string, error) { return "", nil })
	if failures != 1 {
		t.Errorf("failures = %d, want 1: an empty SHA cannot be pinned", failures)
	}
	if !strings.Contains(changes[0], "empty SHA") {
		t.Errorf("changes = %v", changes)
	}
	if entries[0].Ref != "old" {
		t.Errorf("pin must be kept, got %q", entries[0].Ref)
	}
}

var errUnreachable = &unreachableError{}

type unreachableError struct{}

func (*unreachableError) Error() string { return "network unreachable" }

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gallery.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}
