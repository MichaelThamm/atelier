package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelThamm/atelier/internal/session"
)

func TestRepoBasename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git::https://github.com/canonical/observability-stack.git", "observability-stack"},
		{"https://github.com/canonical/observability-stack.git?ref=main", "observability-stack"},
		{"git@github.com:canonical/observability-stack.git", "observability-stack"},
		{"/home/user/local-thing", "local-thing"},
		{"./relative", "relative"},
		{"https://example.com/", "example.com"}, // last segment if no .git
		{"", "repo"},
	}
	for _, c := range cases {
		if got := repoBasename(c.in); got != c.want {
			t.Errorf("repoBasename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestComposeSource(t *testing.T) {
	cases := []struct {
		remote, modulePath, ref, want string
	}{
		{"https://example.com/m.git", "terraform/cos-lite", "v1.2.0",
			"git::https://example.com/m.git//terraform/cos-lite?ref=v1.2.0"},
		{"git::ssh://git@example.com/m.git", "", "main",
			"git::ssh://git@example.com/m.git?ref=main"},
		{"./local", "modules/x", "",
			"./local//modules/x"},
		{"/abs/path", ".", "",
			"/abs/path"},
	}
	for _, c := range cases {
		got := composeSource(c.remote, c.modulePath, c.ref)
		if got != c.want {
			t.Errorf("composeSource(%q, %q, %q) = %q, want %q", c.remote, c.modulePath, c.ref, got, c.want)
		}
	}
}

func TestDecomposeSource(t *testing.T) {
	cases := []struct {
		in, wantURL, wantRef string
	}{
		{"git::https://example.com/m.git//terraform/x?ref=v1", "https://example.com/m.git", "v1"},
		{"https://example.com/m.git?ref=main", "https://example.com/m.git", "main"},
		{"./local//modules/x", "./local", ""},
		{"git::https://example.com/m.git", "https://example.com/m.git", ""},
	}
	for _, c := range cases {
		gotURL, gotRef := decomposeSource(c.in)
		if gotURL != c.wantURL || gotRef != c.wantRef {
			t.Errorf("decomposeSource(%q) = (%q, %q), want (%q, %q)", c.in, gotURL, gotRef, c.wantURL, c.wantRef)
		}
	}
}

func TestModuleBlockName(t *testing.T) {
	cases := []struct{ in, fallback, want string }{
		{"terraform/cos-lite", "", "cos_lite"},
		{"cos", "", "cos"},
		{"terraform/123numeric", "", "m123numeric"},
		{"weird-name!", "", "weird_name"},
		{".", "", "this"},
		{"", "", "this"},
		{".", "terraform-aws-s3-bucket", "terraform_aws_s3_bucket"},
		{".", "observability-stack", "observability_stack"},
	}
	for _, c := range cases {
		if got := ModuleBlockName(c.in, c.fallback); got != c.want {
			t.Errorf("ModuleBlockName(%q, %q) = %q, want %q", c.in, c.fallback, got, c.want)
		}
	}
}

func TestReadRequiredProviders(t *testing.T) {
	dir := t.TempDir()
	const tf = `
terraform {
  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 0.10"
    }
    aws = {
      source = "hashicorp/aws"
    }
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "versions.tf"), []byte(tf), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRequiredProviders(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["juju"].Source != "juju/juju" || got["juju"].Version != ">= 0.10" {
		t.Errorf("juju: %+v", got["juju"])
	}
	if got["aws"].Source != "hashicorp/aws" || got["aws"].Version != "" {
		t.Errorf("aws: %+v", got["aws"])
	}
}

func TestReadRequiredProviders_emptyDir(t *testing.T) {
	got, err := ReadRequiredProviders(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}

func TestPrepareState_localModule(t *testing.T) {
	cloneDir := t.TempDir()
	modPath := "terraform/cos-lite"
	candidateDir := filepath.Join(cloneDir, modPath)
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(candidateDir, "variables.tf"), []byte(`
variable "model_uuid" {
  type = string
}
variable "internal_tls" {
  type    = bool
  default = true
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "versions.tf"), []byte(`
terraform {
  required_providers {
    juju = {
      source  = "juju/juju"
      version = ">= 0.10"
    }
  }
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	wrapperDir := t.TempDir()
	state, err := PrepareState(wrapperDir, cloneDir, modPath, "abc123", "v1.2.0", "https://example.com/m.git")
	if err != nil {
		t.Fatal(err)
	}
	if state.ModuleBlockName != "cos_lite" {
		t.Errorf("ModuleBlockName = %q", state.ModuleBlockName)
	}
	if state.Source != "git::https://example.com/m.git//terraform/cos-lite?ref=v1.2.0" {
		t.Errorf("Source = %q", state.Source)
	}
	if len(state.Vars) != 2 {
		t.Errorf("expected 2 vars, got %d", len(state.Vars))
	}
	if state.RequiredProviders["juju"].Source != "juju/juju" {
		t.Errorf("juju provider not detected: %+v", state.RequiredProviders)
	}
	if len(state.Providers) != 1 || state.Providers[0].Name != "juju" {
		t.Errorf("expected juju provider block, got %+v", state.Providers)
	}
}

func TestCandidatePaths(t *testing.T) {
	// Lightweight check; mainly to ensure the helper compiles cleanly.
	cs := candidatePaths(nil)
	if len(cs) != 0 {
		t.Errorf("nil → non-empty: %v", cs)
	}
}

func TestModulePathFromSource(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git::https://github.com/canonical/observability-stack.git//terraform/cos", "terraform/cos"},
		{"git::https://example.com/m.git//terraform/cos-lite?ref=v1.2.0", "terraform/cos-lite"},
		{"https://example.com/m.git?ref=main", ""},
		{"git::https://example.com/m.git", ""},
		{"./local//modules/x", "modules/x"},
		{"/abs/repo//terraform/cos", "terraform/cos"},
	}
	for _, c := range cases {
		if got := modulePathFromSource(c.in); got != c.want {
			t.Errorf("modulePathFromSource(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestLoadExisting_autoRehydratePreservesSubdir is a regression test for the
// bug where auto-rehydrating a wrapper (no session.json) whose module source
// pointed at a subdirectory (e.g. //terraform/cos) dropped the subdir. That
// left ModuleCandidatePath empty, causing PrepareState to read variables from
// the repository root instead of the module directory — so the primary module
// rendered with zero variables.
func TestLoadExisting_autoRehydratePreservesSubdir(t *testing.T) {
	// Fake "repo" with the module living in a subdirectory.
	repoDir := t.TempDir()
	const modSubdir = "terraform/cos-lite"
	candidateDir := filepath.Join(repoDir, modSubdir)
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "variables.tf"), []byte(`
variable "model_uuid" {
  type = string
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wrapper directory with a main.tf whose module source is a local path
	// carrying a //subdir suffix, and NO session.json (forces auto-rehydrate).
	wrapperDir := t.TempDir()
	mainTF := "module \"cos_lite\" {\n  source = \"" + repoDir + "//" + modSubdir + "\"\n}\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := LoadExisting(context.Background(), wrapperDir, nil)
	if err != nil {
		t.Fatalf("LoadExisting: %v", err)
	}
	if len(res.State.Vars) == 0 {
		t.Fatalf("expected variables to load from %q, got 0 (subdir was dropped)", modSubdir)
	}

	// session.json must persist the recovered subdir for subsequent opens.
	sess, err := session.Load(wrapperDir)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ModuleCandidatePath != modSubdir {
		t.Errorf("ModuleCandidatePath = %q, want %q", sess.ModuleCandidatePath, modSubdir)
	}
}

// TestLoadExisting_recoversEmptyModuleCandidatePath covers wrappers whose
// session.json was written by the earlier buggy auto-rehydrate and therefore
// has an empty module_candidate_path. LoadExisting must re-derive it from the
// module source in main.tf rather than reading the repository root.
func TestLoadExisting_recoversEmptyModuleCandidatePath(t *testing.T) {
	repoDir := t.TempDir()
	const modSubdir = "terraform/cos"
	candidateDir := filepath.Join(repoDir, modSubdir)
	if err := os.MkdirAll(candidateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(candidateDir, "variables.tf"), []byte(`
variable "risk" {
  type    = string
  default = "stable"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	wrapperDir := t.TempDir()
	mainTF := "module \"cos\" {\n  source = \"" + repoDir + "//" + modSubdir + "\"\n}\n"
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-seed a corrupted session.json: empty module_candidate_path.
	if err := session.Save(wrapperDir, &session.Session{
		SourceURL:           repoDir,
		ModuleBlockName:     "cos",
		ModuleCandidatePath: "",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := LoadExisting(context.Background(), wrapperDir, nil)
	if err != nil {
		t.Fatalf("LoadExisting: %v", err)
	}
	if len(res.State.Vars) == 0 {
		t.Fatalf("expected variables to load from %q, got 0 (empty path not recovered)", modSubdir)
	}
	sess, err := session.Load(wrapperDir)
	if err != nil {
		t.Fatal(err)
	}
	if sess.ModuleCandidatePath != modSubdir {
		t.Errorf("ModuleCandidatePath = %q, want %q", sess.ModuleCandidatePath, modSubdir)
	}
}

// TestLoadExisting_deletedRefIsNonFatal is the core robustness guarantee: when
// a wrapper's pinned ref no longer exists on the remote, LoadExisting must NOT
// error. It returns a degraded Result (schema-less State + RefUnresolved) so
// the TUI can still launch and let the user switch refs. It also preserves the
// user's existing values from main.tf and never mutates the wrapper on disk.
func TestLoadExisting_deletedRefIsNonFatal(t *testing.T) {
	wrapperDir := t.TempDir()
	// A remote source pinned to a ref that the remote no longer has.
	mainTF := `module "cos" {
  source = "git::https://example.com/o.git//terraform/cos?ref=feat/old-ref"
  risk   = "candidate"
}
`
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := session.Save(wrapperDir, &session.Session{
		SourceURL:           "https://example.com/o.git",
		LiteralRef:          "feat/old-ref",
		ResolvedSHA:         "deadbeef",
		ModuleBlockName:     "cos",
		ModuleCandidatePath: "terraform/cos",
	}); err != nil {
		t.Fatal(err)
	}

	// ls-remote answers with refs that do NOT include feat/old-ref, so
	// ResolveRef returns *RefNotFoundError before any clone is attempted.
	git := &scriptedGit{stdouts: [][]byte{
		[]byte("aaa\trefs/heads/main\nbbb\trefs/tags/v1.0\n"),
	}}

	res, err := LoadExisting(context.Background(), wrapperDir, git)
	if err != nil {
		t.Fatalf("LoadExisting should be non-fatal on a deleted ref, got: %v", err)
	}
	if res.RefUnresolved == nil {
		t.Fatal("expected RefUnresolved to be set")
	}
	if res.RefUnresolved.Offline {
		t.Error("deleted-ref case should not be flagged Offline")
	}
	if res.RefUnresolved.Ref != "feat/old-ref" {
		t.Errorf("RefUnresolved.Ref = %q, want feat/old-ref", res.RefUnresolved.Ref)
	}
	// The available refs must be offered for recovery.
	wantAvail := map[string]bool{"main": true, "v1.0": true}
	if len(res.RefUnresolved.Available) != len(wantAvail) {
		t.Errorf("Available = %v, want %v", res.RefUnresolved.Available, wantAvail)
	}
	// Degraded state: schema unknown, so Vars is empty. Because we read
	// main.tf without a schema, existing user values are preserved verbatim as
	// raw attributes (UnknownAttrs) rather than typed Values — nothing is lost,
	// and they'll be re-typed against the schema once a valid ref loads.
	if len(res.State.Vars) != 0 {
		t.Errorf("expected empty Vars in degraded state, got %d", len(res.State.Vars))
	}
	var foundRisk bool
	for _, ra := range res.State.UnknownAttrs {
		if ra.Name == "risk" {
			foundRisk = true
		}
	}
	if !foundRisk {
		t.Errorf("expected user value 'risk' preserved as a raw attribute, got UnknownAttrs=%+v", res.State.UnknownAttrs)
	}
	// A clone must never have been attempted.
	if git.ran("clone") {
		t.Error("no clone should be attempted when the ref is unresolvable")
	}
}

// TestLoadExisting_offlineIsNonFatal covers the case where the remote itself
// is unreachable. LoadExisting opens degraded with an Offline banner and no
// ref list (nothing to offer), rather than erroring out.
func TestLoadExisting_offlineIsNonFatal(t *testing.T) {
	wrapperDir := t.TempDir()
	mainTF := `module "cos" {
  source = "git::https://example.com/o.git//terraform/cos?ref=main"
}
`
	if err := os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(mainTF), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := session.Save(wrapperDir, &session.Session{
		SourceURL:           "https://example.com/o.git",
		LiteralRef:          "main",
		ModuleBlockName:     "cos",
		ModuleCandidatePath: "terraform/cos",
	}); err != nil {
		t.Fatal(err)
	}

	// ls-remote fails (network down).
	git := &scriptedGit{
		stdouts: [][]byte{nil},
		errs:    []error{context.DeadlineExceeded},
	}

	res, err := LoadExisting(context.Background(), wrapperDir, git)
	if err != nil {
		t.Fatalf("LoadExisting should be non-fatal when offline, got: %v", err)
	}
	if res.RefUnresolved == nil || !res.RefUnresolved.Offline {
		t.Fatalf("expected RefUnresolved with Offline=true, got %+v", res.RefUnresolved)
	}
	if len(res.RefUnresolved.Available) != 0 {
		t.Errorf("offline case should offer no refs, got %v", res.RefUnresolved.Available)
	}
}

// scriptedGit is a gitops.Runner stub that serves canned stdout per call and
// records the git subcommands it was asked to run, so tests can assert which
// invocations happened (e.g. that a clone was or wasn't issued).
type scriptedGit struct {
	stdouts [][]byte
	errs    []error // optional per-call errors, indexed like stdouts
	calls   [][]string
	idx     int
}

func (s *scriptedGit) Run(_ context.Context, dir string, args ...string) ([]byte, []byte, error) {
	s.calls = append(s.calls, append([]string{dir}, args...))
	var out []byte
	if s.idx < len(s.stdouts) {
		out = s.stdouts[s.idx]
	}
	var err error
	if s.idx < len(s.errs) {
		err = s.errs[s.idx]
	}
	s.idx++
	return out, nil, err
}

// ran reports whether any recorded call's git subcommand matches sub.
func (s *scriptedGit) ran(sub string) bool {
	for _, c := range s.calls {
		// c[0] is the working dir; c[1] is the git subcommand.
		if len(c) > 1 && c[1] == sub {
			return true
		}
	}
	return false
}

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// TestResolveAndClone_reusesCloneAtSameSHA verifies the warm-start fast path:
// when a prior clone already sits at the resolved SHA, ResolveAndClone reuses
// it (ls-remote + rev-parse only) instead of wiping and re-cloning.
func TestResolveAndClone_reusesCloneAtSameSHA(t *testing.T) {
	wrapperDir := t.TempDir()
	cloneDir := filepath.Join(CloneSubdir(wrapperDir), "foo")
	if err := os.MkdirAll(filepath.Join(cloneDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(cloneDir, "marker")
	if err := os.WriteFile(marker, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	git := &scriptedGit{stdouts: [][]byte{
		[]byte(testSHA + "\tHEAD\n"), // ls-remote
		[]byte(testSHA + "\n"),       // rev-parse HEAD
	}}
	dir, sha, err := ResolveAndClone(context.Background(), InitOptions{
		WrapperDir: wrapperDir,
		Source:     "https://example.com/foo.git",
		GitRunner:  git,
	})
	if err != nil {
		t.Fatalf("ResolveAndClone: %v", err)
	}
	if dir != cloneDir {
		t.Errorf("cloneDir = %q, want %q", dir, cloneDir)
	}
	if sha != testSHA {
		t.Errorf("resolvedSHA = %q, want %q", sha, testSHA)
	}
	if git.ran("clone") {
		t.Error("expected no clone on warm-start cache hit")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("existing clone was wiped (marker gone): %v", err)
	}
}

// TestResolveAndClone_reclonesWhenSHADiffers verifies that a stale clone (HEAD
// no longer matches the resolved SHA) is wiped and re-cloned.
func TestResolveAndClone_reclonesWhenSHADiffers(t *testing.T) {
	wrapperDir := t.TempDir()
	cloneDir := filepath.Join(CloneSubdir(wrapperDir), "foo")
	if err := os.MkdirAll(filepath.Join(cloneDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(cloneDir, "marker")
	if err := os.WriteFile(marker, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	const otherSHA = "fedcba9876543210fedcba9876543210fedcba98"
	git := &scriptedGit{stdouts: [][]byte{
		[]byte(testSHA + "\tHEAD\n"), // ls-remote → resolved SHA
		[]byte(otherSHA + "\n"),      // rev-parse HEAD → stale SHA
		nil,                          // clone
	}}
	_, sha, err := ResolveAndClone(context.Background(), InitOptions{
		WrapperDir: wrapperDir,
		Source:     "https://example.com/foo.git",
		GitRunner:  git,
	})
	if err != nil {
		t.Fatalf("ResolveAndClone: %v", err)
	}
	if sha != testSHA {
		t.Errorf("resolvedSHA = %q, want %q", sha, testSHA)
	}
	if !git.ran("clone") {
		t.Error("expected a re-clone when the existing clone is at a different SHA")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("expected stale clone to be wiped, marker stat err = %v", err)
	}
}
