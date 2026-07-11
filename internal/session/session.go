// Package session persists Atelier's in-band metadata to
// .atelier/session.json — the slimmest record needed to detect ref bumps
// (SPEC §5.4) and rehydrate the session.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Session records data we want to survive across invocations. The schema is
// JSON for ease of inspection / hand-debugging.
type Session struct {
	// SourceURL is the canonical git URL of the module repository, before
	// the ref suffix. Example:
	//   "git::https://github.com/canonical/observability-stack.git"
	SourceURL string `json:"source_url"`

	// LiteralRef is the user-supplied ref string ("main", "v1.2.0", a SHA).
	LiteralRef string `json:"literal_ref"`

	// ResolvedSHA is the SHA that LiteralRef pointed to at the end of the
	// previous session. Comparing the current resolution against this is how
	// Atelier detects ref bumps (SPEC §5.4).
	ResolvedSHA string `json:"resolved_sha"`

	// ModuleCandidatePath is the candidate path within the cloned repo.
	ModuleCandidatePath string `json:"module_candidate_path"`

	// ModuleBlockName is the HCL block name (e.g. "cos_lite").
	ModuleBlockName string `json:"module_block_name"`

	// LastOpened is the timestamp of the most recent successful open.
	LastOpened time.Time `json:"last_opened"`
}

// Path returns the path to session.json inside a wrapper directory.
func Path(wrapperDir string) string {
	return filepath.Join(wrapperDir, ".atelier", "session.json")
}

// Load reads session.json from `wrapperDir`. A missing file is NOT an
// error: it returns (nil, nil) so the caller can treat "no session" as a
// regular branch.
func Load(wrapperDir string) (*Session, error) {
	data, err := os.ReadFile(Path(wrapperDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session.json: %w", err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse session.json: %w", err)
	}
	return &s, nil
}

// Save writes session.json into the wrapper's .atelier/ directory, creating
// the directory if necessary. The write is atomic via temp-file rename.
func Save(wrapperDir string, s *Session) error {
	dir := filepath.Join(wrapperDir, ".atelier")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "session-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, Path(wrapperDir)); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// RefBumpedSince reports whether the resolved SHA has changed since the
// session was last saved.
func (s *Session) RefBumpedSince(currentSHA string) bool {
	return s != nil && s.ResolvedSHA != "" && currentSHA != "" && s.ResolvedSHA != currentSHA
}
