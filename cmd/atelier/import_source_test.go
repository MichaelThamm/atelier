package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- checkImportTarget: which directory --dir may name ---

// With --source a wrapper is bootstrapped into the target, so a --dir that does
// not exist yet is allowed — as it is for `atelier apply` — and setupSourceModule
// creates it. Refusing it only pushed a `mkdir` out of the command.
func TestCheckImportTarget_bootstrapsMissingDir(t *testing.T) {
	base := t.TempDir()
	if err := checkImportTarget(filepath.Join(base, "stack"), true); err != nil {
		t.Errorf("a missing --dir with --source must be allowed: %v", err)
	}
}

// A directory holding files is refused rather than scaffolded over, exactly as
// `atelier apply` refuses one. The remedy names --dir, the only flag import has.
func TestCheckImportTarget_refusesNonEmptyWithoutMainTF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := checkImportTarget(dir, true)
	if err == nil {
		t.Fatal("expected an error for a non-empty target")
	}
	if !strings.Contains(err.Error(), "--dir") {
		t.Errorf("error = %q, want it to name --dir", err)
	}
	if strings.Contains(err.Error(), "--as") {
		t.Errorf("error = %q, must not name --as: import has no such flag", err)
	}
}

// A directory with a hand-authored main.tf is adopted (setupSourceModule
// re-hydrates it), so the non-empty refusal must not fire.
func TestCheckImportTarget_adoptsExistingMainTF(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkImportTarget(dir, true); err != nil {
		t.Errorf("an existing main.tf must be adopted, not refused: %v", err)
	}
}

// Without --source nothing is scaffolded, so a missing --dir has nothing to
// become a Terraform root.
func TestCheckImportTarget_requiresExistingRootWithoutSource(t *testing.T) {
	base := t.TempDir()
	err := checkImportTarget(filepath.Join(base, "stack"), false)
	if err == nil {
		t.Fatal("expected an error for a missing --dir without --source")
	}
	if !strings.Contains(err.Error(), "Terraform root") {
		t.Errorf("error = %q, want it to say what the directory must be", err)
	}

	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkImportTarget(empty, false); err != nil {
		t.Errorf("an existing directory is importable without --source: %v", err)
	}
}

// --- sourceConflict: --source/--module/--ref against a wrapper's source ---

func TestSourceConflict(t *testing.T) {
	declared := "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=track/3.0"
	cases := []struct {
		name      string
		declared  string // defaults to the COS Lite address above
		requested string
		want      string // "" means no conflict
	}{
		{
			name:      "identical",
			requested: "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=track/3.0",
		},
		{
			name:      "a bare remote matches the git:: form",
			requested: "https://github.com/canonical/observability-stack.git",
		},
		{
			name:      "a different remote conflicts",
			requested: "https://github.com/canonical/loki-operators.git//terraform/cos-lite?ref=track/3.0",
			want:      "the source is https://github.com/canonical/loki-operators.git",
		},
		{
			name:      "a different module path conflicts",
			requested: "https://github.com/canonical/observability-stack.git//terraform/cos?ref=track/3.0",
			want:      "--module terraform/cos,",
		},
		{
			name:      "a different ref conflicts",
			requested: "https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main",
			want:      "--ref main,",
		},
		{
			name:      "a ref where the wrapper pins none conflicts",
			declared:  "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite",
			requested: "https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=main",
			want:      "the wrapper pins none",
		},
		{
			name:      "a module where the wrapper uses the repository root conflicts",
			declared:  "git::https://github.com/canonical/observability-stack.git?ref=track/3.0",
			requested: "https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=track/3.0",
			want:      "repository root",
		},
		{
			name:      "a local path is compared literally",
			requested: "./my-module",
			want:      "my-module",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			declared := declared
			if c.declared != "" {
				declared = c.declared
			}
			got := sourceConflict(c.requested, declared)
			if c.want == "" {
				if got != "" {
					t.Errorf("sourceConflict = %q, want no conflict", got)
				}
				return
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("sourceConflict = %q, want it to mention %q", got, c.want)
			}
		})
	}
}

// An omitted --module or --ref is not a request to change the wrapper's: the
// import runs against what main.tf declares, so only the components actually
// supplied are compared.
func TestSourceConflict_ignoresOmittedComponents(t *testing.T) {
	declared := "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=track/3.0"
	for _, requested := range []string{
		"https://github.com/canonical/observability-stack.git",
		"git::https://github.com/canonical/observability-stack.git",
		"https://github.com/canonical/observability-stack.git//terraform/cos-lite",
	} {
		if c := sourceConflict(requested, declared); c != "" {
			t.Errorf("sourceConflict(%q) = %q, want no conflict", requested, c)
		}
	}
}

// --- checkImportTargetSource: the wrapper the target already holds ---

func wrapperWithSource(t *testing.T, sources ...string) string {
	t.Helper()
	dir := t.TempDir()
	body := ""
	for i, s := range sources {
		body += "\nmodule \"m" + string(rune('a'+i)) + "\" {\n  source = \"" + s + "\"\n}\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheckImportTargetSource(t *testing.T) {
	const (
		cosLite = "git::https://github.com/canonical/observability-stack.git//terraform/cos-lite?ref=track/3.0"
		loki    = "git::https://github.com/canonical/loki-operators.git//terraform/loki?ref=2.0"
	)
	cases := []struct {
		name       string
		dir        string
		source     string
		modulePath string
		ref        string
		wantErr    string
	}{
		{
			name: "no flags at all is not a contradiction",
			dir:  wrapperWithSource(t, cosLite), source: "", modulePath: "", ref: "",
		},
		{
			name:   "the same address re-stated is fine",
			dir:    wrapperWithSource(t, cosLite),
			source: "https://github.com/canonical/observability-stack.git", modulePath: "terraform/cos-lite", ref: "track/3.0",
		},
		{
			name:    "a ref the wrapper does not pin is refused",
			dir:     wrapperWithSource(t, cosLite),
			source:  "https://github.com/canonical/observability-stack.git",
			ref:     "main",
			wantErr: "--ref main",
		},
		{
			name:       "a different module is refused",
			dir:        wrapperWithSource(t, cosLite),
			source:     "https://github.com/canonical/observability-stack.git",
			modulePath: "terraform/cos",
			wantErr:    "--module terraform/cos,",
		},
		{
			name:    "a different module in a multi-module wrapper is refused",
			dir:     wrapperWithSource(t, cosLite, loki),
			source:  "https://github.com/canonical/observability-stack.git",
			ref:     "main",
			wantErr: "--ref main",
		},
		{
			name:       "one of several blocks agreeing is enough",
			dir:        wrapperWithSource(t, loki, cosLite),
			source:     "https://github.com/canonical/observability-stack.git",
			modulePath: "terraform/cos-lite",
			ref:        "track/3.0",
		},
		{
			name:   "a root with no module block has nothing to contradict",
			dir:    t.TempDir(),
			source: "https://github.com/canonical/observability-stack.git",
			ref:    "main",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkImportTargetSource(c.dir, c.source, c.modulePath, c.ref)
			if c.wantErr == "" {
				if err != nil {
					t.Errorf("checkImportTargetSource: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error mentioning %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantErr)
			}
			// The message must say what did NOT happen and how to proceed.
			for _, want := range []string{"main.tf", "--dir"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}
