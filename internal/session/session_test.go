package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &Session{
		SourceURL:           "git::https://example.com/m.git",
		LiteralRef:          "main",
		ResolvedSHA:         "abc123",
		ModuleCandidatePath: "terraform/cos-lite",
		ModuleBlockName:     "cos_lite",
		LastOpened:          time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC),
	}
	if err := Save(dir, in); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceURL != in.SourceURL || got.LiteralRef != in.LiteralRef ||
		got.ResolvedSHA != in.ResolvedSHA || got.ModuleCandidatePath != in.ModuleCandidatePath ||
		got.ModuleBlockName != in.ModuleBlockName {
		t.Errorf("round trip mismatch: in=%+v got=%+v", in, got)
	}
	if !got.LastOpened.Equal(in.LastOpened) {
		t.Errorf("LastOpened: got %v, want %v", got.LastOpened, in.LastOpened)
	}
}

func TestLoad_missing_returnsNil(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil session for missing file, got %+v", got)
	}
}

func TestRefBumpedSince(t *testing.T) {
	cases := []struct {
		name     string
		s        *Session
		current  string
		want     bool
	}{
		{"nil session", nil, "abc", false},
		{"empty current", &Session{ResolvedSHA: "abc"}, "", false},
		{"empty resolved", &Session{ResolvedSHA: ""}, "abc", false},
		{"same", &Session{ResolvedSHA: "abc"}, "abc", false},
		{"different", &Session{ResolvedSHA: "abc"}, "def", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.s.RefBumpedSince(c.current); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestPath_format(t *testing.T) {
	dir := "/some/wrapper"
	got := Path(dir)
	want := filepath.Join(dir, ".atelier", "session.json")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
