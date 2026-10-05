package bootstrap

import (
	"os"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/session"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// newRefState is the state a compose writes: the block it re-pointed, carrying
// the values it read back from main.tf.
func newRefState(t *testing.T, blockName, source string) *wrapper.State {
	t.Helper()
	return &wrapper.State{
		ModuleBlockName: blockName,
		Source:          source,
		Values:          map[string]cty.Value{},
	}
}

// ReconcileSession exists because a CLI re-point that left session.json alone
// would be reverted: LoadExisting trusts the session over main.tf, so the TUI
// reopens on the previous ref and the next save writes that ref back.
func TestReconcileSession(t *testing.T) {
	const src = "git::https://github.com/canonical/mimir-operators.git//terraform?ref=rev300"

	cases := []struct {
		name       string
		prior      *session.Session
		block      string
		wantSaved  bool
		wantRef    string
		wantPath   string
		wantSource string
	}{
		{
			name: "the tracked block is re-pointed",
			prior: &session.Session{
				SourceURL:           "git::https://github.com/canonical/mimir-operators.git",
				LiteralRef:          "main",
				ResolvedSHA:         "oldsha",
				ModuleCandidatePath: "terraform",
				ModuleBlockName:     "mimir",
			},
			block:     "mimir",
			wantSaved: true,
			wantRef:   "rev300",
			wantPath:  "terraform",
		},
		{
			// A wrapper Atelier has never opened has no session to correct, and
			// apply must not start inventing them.
			name:      "no session is created",
			prior:     nil,
			block:     "mimir",
			wantSaved: false,
		},
		{
			// Only the primary owns the session; a secondary block's source is
			// tracked entirely by main.tf, so rewriting it would move the
			// primary's recorded identity.
			name: "another block is left alone",
			prior: &session.Session{
				SourceURL:       "git::https://github.com/canonical/mimir-operators.git",
				LiteralRef:      "main",
				ModuleBlockName: "mimir",
			},
			block:     "loki",
			wantSaved: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.prior != nil {
				if err := session.Save(dir, c.prior); err != nil {
					t.Fatal(err)
				}
			}
			state := newRefState(t, "mimir", src)

			if err := ReconcileSession(dir, c.block, state, "newsha"); err != nil {
				t.Fatalf("ReconcileSession: %v", err)
			}

			got, err := session.Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if c.prior == nil {
				if got != nil {
					t.Fatalf("a session was invented: %+v", got)
				}
				if _, err := os.Stat(session.Path(dir)); err == nil {
					t.Error("session.json was written for a wrapper that had none")
				}
				return
			}
			if !c.wantSaved {
				if got.SourceURL != c.prior.SourceURL || got.LiteralRef != c.prior.LiteralRef {
					t.Errorf("session was rewritten for another block: %+v", got)
				}
				return
			}
			if got.LiteralRef != c.wantRef {
				t.Errorf("LiteralRef = %q, want %q", got.LiteralRef, c.wantRef)
			}
			if got.ModuleCandidatePath != c.wantPath {
				t.Errorf("ModuleCandidatePath = %q, want %q", got.ModuleCandidatePath, c.wantPath)
			}
			if got.ResolvedSHA != "newsha" {
				t.Errorf("ResolvedSHA = %q, want newsha", got.ResolvedSHA)
			}
			// Decompose strips the git:: prefix with the ref and sub-path. The
			// session is rehydrated through Compose, which re-adds it, so the
			// round-trip is stable — this asserts the prefix is not doubled.
			const wantURL = "https://github.com/canonical/mimir-operators.git"
			if got.SourceURL != wantURL {
				t.Errorf("SourceURL = %q, want %q", got.SourceURL, wantURL)
			}
		})
	}
}

// An unpinned block must reconcile to an unpinned session, not keep the old ref:
// the previous ref was the thing being removed.
func TestReconcileSession_clearsARefThatWasDropped(t *testing.T) {
	dir := t.TempDir()
	prev := &session.Session{
		SourceURL:       "git::https://github.com/canonical/mimir-operators.git",
		LiteralRef:      "rev300",
		ModuleBlockName: "mimir",
	}
	if err := session.Save(dir, prev); err != nil {
		t.Fatal(err)
	}
	unpinned := "git::https://github.com/canonical/mimir-operators.git//terraform"

	if err := ReconcileSession(dir, "mimir", newRefState(t, "mimir", unpinned), "newsha"); err != nil {
		t.Fatalf("ReconcileSession: %v", err)
	}
	got, err := session.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.LiteralRef != "" {
		t.Errorf("LiteralRef = %q, want empty for an unpinned block", got.LiteralRef)
	}
}
