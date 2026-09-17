package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hasConcern reports whether any concern at or above the given level mentions
// the substring.
func hasConcern(concerns []concern, level concernLevel, substr string) bool {
	for _, c := range concerns {
		if c.level >= level && strings.Contains(c.detail, substr) {
			return true
		}
	}
	return false
}

func maxLevel(concerns []concern) concernLevel {
	m := levelNote
	for _, c := range concerns {
		if c.level > m {
			m = c.level
		}
	}
	return m
}

func TestInspectTarget_emptyDirIsUnremarkable(t *testing.T) {
	if got := inspectTarget(t.TempDir()); len(got) != 0 {
		t.Errorf("expected no concerns for an empty directory; got %+v", got)
	}
}

func TestInspectTarget_benignEntriesDoNotWarn(t *testing.T) {
	dir := t.TempDir()
	// Exactly the furniture Atelier authors or tolerates. Warning here would
	// make the prompt fire on every re-run and train users to ignore it.
	for _, name := range []string{"README.md", "LICENSE", ".gitignore", "terraform.tfstate"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".git", ".atelier", ".terraform"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := inspectTarget(dir); maxLevel(got) >= levelWarn {
		t.Errorf("expected no warnings for benign entries; got %+v", got)
	}
}

func TestInspectTarget_nonEmptyWarns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := inspectTarget(dir)
	if !hasConcern(got, levelWarn, "not empty") {
		t.Errorf("expected a non-empty warning; got %+v", got)
	}
}

func TestInspectTarget_foreignProjectMarkerAlarms(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := inspectTarget(dir)
	if !hasConcern(got, levelAlarm, "another project") {
		t.Errorf("expected an alarm about another project; got %+v", got)
	}
}

func TestInspectTarget_handAuthoredTerraformAlarms(t *testing.T) {
	dir := t.TempDir()
	body := "resource \"null_resource\" \"x\" {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := inspectTarget(dir)
	if !hasConcern(got, levelAlarm, "hand-authored") {
		t.Errorf("expected an alarm about hand-authored Terraform; got %+v", got)
	}
}

func TestInspectTarget_wrapperLikeTerraformWarnsButDoesNotAlarm(t *testing.T) {
	dir := t.TempDir()
	body := "module \"cos\" {\n  source = \"git::https://example.com/m.git?ref=v1\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := inspectTarget(dir)
	if !hasConcern(got, levelWarn, "Terraform files") {
		t.Errorf("expected a warning about existing Terraform files; got %+v", got)
	}
	if hasConcern(got, levelAlarm, "hand-authored") {
		t.Errorf("a wrapper-shaped main.tf should not be reported as hand-authored; got %+v", got)
	}
}

func TestInspectTarget_declarationCollisionsAreNotes(t *testing.T) {
	dir := t.TempDir()
	body := "terraform {\n  required_providers {\n    juju = { source = \"juju/juju\" }\n  }\n}\n\nprovider \"juju\" {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := inspectTarget(dir)
	if !hasConcern(got, levelNote, "required_providers") {
		t.Errorf("expected a note about required_providers; got %+v", got)
	}
	if !hasConcern(got, levelNote, "provider configuration") {
		t.Errorf("expected a note about the kept provider configuration; got %+v", got)
	}
}

func TestInspectTarget_nestedWrapperAlarms(t *testing.T) {
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "main.tf"), []byte("# wrapper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outer, ".atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "sub")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	got := inspectTarget(inner)
	if !hasConcern(got, levelAlarm, "inside an existing wrapper") {
		t.Errorf("expected an alarm about nesting; got %+v", got)
	}
}

func TestInspectTarget_nestedWrapperStopsAtRepoBoundary(t *testing.T) {
	// A wrapper above a repository boundary is unrelated to this checkout, so
	// the walk must not report it.
	outer := t.TempDir()
	if err := os.WriteFile(filepath.Join(outer, "main.tf"), []byte("# wrapper\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outer, ".atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(repo, "infra")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := inspectTarget(inner); hasConcern(got, levelAlarm, "inside an existing wrapper") {
		t.Errorf("walk crossed a repository boundary; got %+v", got)
	}
}

func TestIsWrapperDir(t *testing.T) {
	dir := t.TempDir()
	if isWrapperDir(dir) {
		t.Error("empty dir is not a wrapper")
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isWrapperDir(dir) {
		t.Error("main.tf alone is not a wrapper: it may be someone's Terraform root")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".atelier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !isWrapperDir(dir) {
		t.Error("main.tf + .atelier/ is a wrapper")
	}
}

func TestConfirmTargetDir_yesSkipsPromptOnWarnings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, err := confirmTargetDir(dir, "Bootstrap in", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("--yes should proceed despite warnings")
	}
}

func TestConfirmTargetDir_cleanDirNeedsNoPrompt(t *testing.T) {
	// No --yes and no terminal: this must still succeed, proving a clean
	// directory never reaches the prompt.
	ok, err := confirmTargetDir(t.TempDir(), "Bootstrap in", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("a clean directory should proceed without confirmation")
	}
}

func TestConfirmTargetDir_failsClosedWithoutTerminal(t *testing.T) {
	// Under `go test`, stdin is not a character device — the same condition as
	// CI. Warnings must abort with an actionable error rather than silently
	// proceeding or blocking on a read.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isTerminal(os.Stdin) {
		t.Skip("stdin is a terminal; this case cannot be exercised here")
	}
	ok, err := confirmTargetDir(dir, "Bootstrap in", false)
	if err == nil {
		t.Fatal("expected an error when there is no terminal to prompt on")
	}
	if ok {
		t.Error("must not proceed without confirmation")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should name the escape hatch; got: %v", err)
	}
}

func TestSummariseEntries_bounded(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e", "f", "g"}
	got := summariseEntries(names)
	if !strings.Contains(got, "and 2 more") {
		t.Errorf("expected a bounded summary; got %q", got)
	}
	if got := summariseEntries([]string{"a", "b"}); got != "a, b" {
		t.Errorf("short lists should be printed in full; got %q", got)
	}
}

func TestParseModuleAddArgs_yesFlag(t *testing.T) {
	for _, flag := range []string{"--yes", "-y"} {
		opts, err := parseModuleAddArgs([]string{"https://example.com/m.git", flag})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", flag, err)
		}
		if !opts.Yes {
			t.Errorf("%s: expected Yes to be set", flag)
		}
	}
	opts, err := parseModuleAddArgs([]string{"https://example.com/m.git"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Yes {
		t.Error("Yes should default to false")
	}
}
