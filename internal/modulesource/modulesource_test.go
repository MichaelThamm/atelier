package modulesource

import "testing"

func TestDecompose(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantURL string
		wantRef string
	}{
		{"plain https", "https://github.com/org/repo.git", "https://github.com/org/repo.git", ""},
		{"with ref", "https://github.com/org/repo.git?ref=v1.2.0", "https://github.com/org/repo.git", "v1.2.0"},
		{"git prefix", "git::https://github.com/org/repo.git?ref=main", "https://github.com/org/repo.git", "main"},
		{"subpath", "https://github.com/org/repo.git//modules/cos-lite?ref=v1", "https://github.com/org/repo.git", "v1"},
		{"git prefix and subpath", "git::https://github.com/org/repo.git//subdir?ref=abc123", "https://github.com/org/repo.git", "abc123"},
		{"no scheme", "github.com/org/repo.git", "github.com/org/repo.git", ""},
		{"ssh", "git::ssh://git@example.com/m.git?ref=main", "ssh://git@example.com/m.git", "main"},
		{"local subpath", "./local//modules/x", "./local", ""},
		{"root", "git::https://example.com/m.git", "https://example.com/m.git", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url, ref := Decompose(c.in)
			if url != c.wantURL || ref != c.wantRef {
				t.Errorf("Decompose(%q) = (%q, %q), want (%q, %q)", c.in, url, ref, c.wantURL, c.wantRef)
			}
		})
	}
}

func TestRemote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git::https://example.com/m.git//subdir?ref=v1", "https://example.com/m.git"},
		{"https://example.com/m.git?ref=main", "https://example.com/m.git"},
		{"./local//modules/x", "./local"},
	}
	for _, c := range cases {
		if got := Remote(c.in); got != c.want {
			t.Errorf("Remote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestModulePath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"root", "https://github.com/org/repo.git", ""},
		{"subpath", "https://github.com/org/repo.git//modules/cos-lite?ref=v1", "modules/cos-lite"},
		{"git prefix", "git::https://github.com/org/repo.git//subdir", "subdir"},
		{"deep", "https://github.com/org/repo.git//a/b/c", "a/b/c"},
		{"ref only", "git::https://example.com/m.git?ref=v1.2.0", ""},
		{"git prefix root", "git::https://example.com/m.git", ""},
		{"local subpath", "./local//modules/x", "modules/x"},
		{"absolute subpath", "/abs/repo//terraform/cos", "terraform/cos"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModulePath(c.in); got != c.want {
				t.Errorf("ModulePath(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCompose(t *testing.T) {
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
		if got := Compose(c.remote, c.modulePath, c.ref); got != c.want {
			t.Errorf("Compose(%q, %q, %q) = %q, want %q", c.remote, c.modulePath, c.ref, got, c.want)
		}
	}
}

func TestDecomposeComposeRoundTrip(t *testing.T) {
	src := Compose("https://github.com/org/repo.git", "terraform/cos-lite", "v1.2.0")
	url, ref := Decompose(src)
	if url != "https://github.com/org/repo.git" || ref != "v1.2.0" {
		t.Errorf("round trip: Decompose(%q) = (%q, %q)", src, url, ref)
	}
	if got := ModulePath(src); got != "terraform/cos-lite" {
		t.Errorf("round trip: ModulePath(%q) = %q", src, got)
	}
}

func TestRepoBasename(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git::https://github.com/canonical/observability-stack.git", "observability-stack"},
		{"https://github.com/canonical/observability-stack.git?ref=main", "observability-stack"},
		{"git@github.com:canonical/observability-stack.git", "observability-stack"},
		{"/home/user/local-thing", "local-thing"},
		{"./relative", "relative"},
		{"https://example.com/", "example.com"}, // last segment if no .git
		{"", "repo"},
		{".", "repo"},
		{"..", "repo"},
	}
	for _, c := range cases {
		if got := RepoBasename(c.in); got != c.want {
			t.Errorf("RepoBasename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsLocal(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/home/user/module", true},
		{"./module", true},
		{"../module", true},
		{"/absolute", true},
		{"https://github.com/org/repo.git", false},
		{"git@github.com:org/repo.git", false},
		{"git::https://example.com/repo.git?ref=v1.0", false},
		{"registry.terraform.io/hashicorp/aws", false},
		{"github.com/hashicorp/terraform-aws/modules/vpc?ref=v5.0", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsLocal(c.in); got != c.want {
			t.Errorf("IsLocal(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsFullSHA(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"valid", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", true},
		{"uppercase", "A1B2C3D4E5F6A1B2C3D4E5F6A1B2C3D4E5F6A1B2", false},
		{"too short", "a1b2c3", false},
		{"too long", "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b20", false},
		{"non-hex", "g1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsFullSHA(c.in); got != c.want {
				t.Errorf("IsFullSHA(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
