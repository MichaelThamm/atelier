package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParseLsRemote(t *testing.T) {
	out := []byte(`abc123def456abc123def456abc123def456abcd	HEAD
abc123def456abc123def456abc123def456abcd	refs/heads/main
deadbeefdeadbeefdeadbeefdeadbeefdeadbeef	refs/heads/dev
1111111111111111111111111111111111111111	refs/tags/v1.0
2222222222222222222222222222222222222222	refs/tags/v1.0^{}
`)
	refs := parseLsRemote(out)
	if refs["HEAD"] != "abc123def456abc123def456abc123def456abcd" {
		t.Errorf("HEAD: %q", refs["HEAD"])
	}
	if refs["refs/heads/main"] != "abc123def456abc123def456abc123def456abcd" {
		t.Errorf("main: %q", refs["refs/heads/main"])
	}
	if refs["refs/tags/v1.0^{}"] != "2222222222222222222222222222222222222222" {
		t.Errorf("v1.0 peeled: %q", refs["refs/tags/v1.0^{}"])
	}
}

func TestParseLsRemote_ignoresBlankAndMalformed(t *testing.T) {
	out := []byte("abc\trefs/heads/x\n\n   \nno-tabs-here\n")
	refs := parseLsRemote(out)
	if len(refs) != 1 {
		t.Errorf("expected 1 ref, got %d: %+v", len(refs), refs)
	}
}

func TestResolveRef_directHit(t *testing.T) {
	refs := map[string]string{
		"refs/heads/main":  "aaa",
		"refs/tags/v1":     "bbb",
		"refs/tags/v1^{}":  "ccc",
		"refs/heads/feat":  "ddd",
		"refs/pull/42/head": "eee",
	}
	cases := []struct {
		in, want string
	}{
		{"main", "aaa"},
		{"refs/heads/main", "aaa"},
		{"v1", "bbb"},
		{"feat", "ddd"},
		{"refs/pull/42/head", "eee"},
	}
	for _, c := range cases {
		got, err := ResolveRef(c.in, refs)
		if err != nil {
			t.Errorf("ResolveRef(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ResolveRef(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveRef_shaPassthrough(t *testing.T) {
	sha := "abcdef0123456789abcdef0123456789abcdef01"
	got, err := ResolveRef(sha, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != sha {
		t.Errorf("got %q", got)
	}
}

func TestResolveRef_notFound(t *testing.T) {
	if _, err := ResolveRef("nope", map[string]string{"refs/heads/x": "y"}); err == nil {
		t.Error("expected error")
	}
}

func TestIsHexSHA(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"abcdef0123456789abcdef0123456789abcdef01", true},
		{"abc1234", true},
		{"abcdef0z", false},
		{"main", false},
		{"v1.2.0", false},
	}
	for _, c := range cases {
		if got := isHexSHA(c.s); got != c.want {
			t.Errorf("isHexSHA(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

// fakeRunner records calls and serves canned responses.
type fakeRunner struct {
	calls    [][]string
	stdouts  [][]byte
	stderrs  [][]byte
	errs     []error
	callIdx  int
}

func (f *fakeRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string{dir}, args...))
	if f.callIdx >= len(f.stdouts) {
		return nil, nil, errors.New("fakeRunner: out of canned responses")
	}
	stdout := f.stdouts[f.callIdx]
	var stderr []byte
	if f.callIdx < len(f.stderrs) {
		stderr = f.stderrs[f.callIdx]
	}
	var err error
	if f.callIdx < len(f.errs) {
		err = f.errs[f.callIdx]
	}
	f.callIdx++
	return stdout, stderr, err
}

func TestLsRemote_passesURLToGit(t *testing.T) {
	out := []byte("abc\trefs/heads/main\n")
	r := &fakeRunner{stdouts: [][]byte{out}}
	refs, err := LsRemote(context.Background(), r, "https://example.com/foo.git")
	if err != nil {
		t.Fatal(err)
	}
	if refs["refs/heads/main"] != "abc" {
		t.Errorf("got %v", refs)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	c := r.calls[0]
	if c[1] != "ls-remote" || c[2] != "https://example.com/foo.git" {
		t.Errorf("call args: %v", c)
	}
}

func TestLsRemote_errorPropagated(t *testing.T) {
	r := &fakeRunner{stdouts: [][]byte{nil}, stderrs: [][]byte{[]byte("not found")}, errs: []error{errors.New("exit 128")}}
	if _, err := LsRemote(context.Background(), r, "https://example.com/missing.git"); err == nil {
		t.Error("expected error")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error did not include stderr: %v", err)
	}
}

func TestClone_branchRef_invokesGitClone(t *testing.T) {
	r := &fakeRunner{stdouts: [][]byte{nil}}
	err := Clone(context.Background(), r, CloneOptions{
		URL:    "https://example.com/m.git",
		Ref:    "main",
		Target: "/tmp/atelier-clone-test-zzz-doesnotexist",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls: %+v", r.calls)
	}
	args := r.calls[0]
	// args[0] is dir; we expect: "", "clone", "--depth=1", "--branch", "main", URL, target
	if args[1] != "clone" || args[2] != "--depth=1" {
		t.Errorf("args: %v", args)
	}
	if args[3] != "--branch" || args[4] != "main" {
		t.Errorf("missing --branch main: %v", args)
	}
}

func TestClone_shaRef_followsUpFetchAndCheckout(t *testing.T) {
	// Use a non-existent target so the pre-check passes.
	target := "/tmp/atelier-clone-test-yyy-doesnotexist"
	r := &fakeRunner{
		stdouts: [][]byte{nil, nil, nil},
	}
	err := Clone(context.Background(), r, CloneOptions{
		URL:    "https://example.com/m.git",
		Ref:    "abcdef0123456789abcdef0123456789abcdef01",
		Target: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 3 {
		t.Fatalf("expected 3 calls (clone, fetch, checkout), got %d: %+v", len(r.calls), r.calls)
	}
	if r.calls[1][1] != "fetch" {
		t.Errorf("expected fetch, got %v", r.calls[1])
	}
	if r.calls[2][1] != "checkout" {
		t.Errorf("expected checkout, got %v", r.calls[2])
	}
}

func TestClone_subdir_partialSparseCheckout(t *testing.T) {
	r := &fakeRunner{stdouts: [][]byte{nil, nil}}
	err := Clone(context.Background(), r, CloneOptions{
		URL:    "https://example.com/m.git",
		Ref:    "main",
		Target: "/tmp/atelier-clone-test-sparse-doesnotexist",
		Subdir: "terraform/cos",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 calls (clone, sparse-checkout set), got %d: %+v", len(r.calls), r.calls)
	}
	clone := r.calls[0]
	if !containsArg(clone, "--filter=blob:none") || !containsArg(clone, "--sparse") {
		t.Errorf("clone missing partial/sparse flags: %v", clone)
	}
	if !containsArg(clone, "--branch") {
		t.Errorf("clone missing --branch: %v", clone)
	}
	sc := r.calls[1]
	// args: dir, "sparse-checkout", "set", "terraform/cos"
	if sc[1] != "sparse-checkout" || sc[2] != "set" || sc[len(sc)-1] != "terraform/cos" {
		t.Errorf("unexpected sparse-checkout call: %v", sc)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

