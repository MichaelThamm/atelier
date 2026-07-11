// Package gitops shells out to the system `git` binary to clone module
// repositories and resolve refs. v1 supports public repos only
// (ADR-0003). The package isolates Atelier from git's exact CLI syntax and
// makes the resulting flow testable: parsing logic (e.g. of `git ls-remote`
// output) lives here without touching the network.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Runner is an interface for executing git commands. The default
// implementation shells out; tests inject a stub.
type Runner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout, stderr []byte, err error)
}

// Git is the default runner backed by os/exec.
type Git struct {
	// Path to the git binary. If empty, "git" is used (looked up via $PATH).
	Path string
}

// Run runs `git <args...>` in `dir`. dir may be empty for commands that
// don't operate on a working tree (e.g. ls-remote).
func (g *Git) Run(ctx context.Context, dir string, args ...string) ([]byte, []byte, error) {
	bin := g.Path
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", // never prompt for credentials in v1
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// LsRemote runs `git ls-remote <url>` and parses the output into a map of
// ref → SHA. Refs are kept in their full form (e.g. "refs/heads/main",
// "refs/tags/v1.2.0"). HEAD is included as a separate entry.
func LsRemote(ctx context.Context, r Runner, url string) (map[string]string, error) {
	stdout, stderr, err := r.Run(ctx, "", "ls-remote", url)
	if err != nil {
		return nil, fmt.Errorf("git ls-remote %s: %w (%s)", url, err, strings.TrimSpace(string(stderr)))
	}
	return parseLsRemote(stdout), nil
}

// parseLsRemote is the pure parsing half of LsRemote, exposed for testing
// without spawning git. Each output line is "<sha>\t<ref>".
func parseLsRemote(out []byte) map[string]string {
	refs := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		sha := strings.TrimSpace(parts[0])
		ref := strings.TrimSpace(parts[1])
		if sha == "" || ref == "" {
			continue
		}
		refs[ref] = sha
	}
	return refs
}

// RefNotFoundError is returned by ResolveRef when a user-supplied ref cannot
// be matched against any of the remote's refs (e.g. the branch/tag was
// deleted). It carries the human-friendly names of the refs that DO exist so
// callers can offer them as recovery options instead of leaving the user
// guessing. This is a distinct, recoverable condition — the remote was
// reachable and answered; the ref simply no longer exists.
type RefNotFoundError struct {
	Ref       string   // the ref the user asked for
	Available []string // human-friendly available ref names (branches + tags)
}

func (e *RefNotFoundError) Error() string {
	return fmt.Sprintf("ref %q not found among %d remote refs", e.Ref, len(e.Available))
}

// AvailableRefNames extracts the human-friendly ref names (branches and tags)
// from an ls-remote map, stripping the refs/heads/ and refs/tags/ prefixes and
// dropping symbolic/peeled entries (HEAD, ^{}). The result is sorted for
// stable presentation.
func AvailableRefNames(refs map[string]string) []string {
	var names []string
	for ref := range refs {
		switch {
		case strings.HasSuffix(ref, "^{}"):
			// Peeled annotated tag; the un-peeled entry already covers it.
			continue
		case ref == "HEAD":
			continue
		case strings.HasPrefix(ref, "refs/heads/"):
			names = append(names, strings.TrimPrefix(ref, "refs/heads/"))
		case strings.HasPrefix(ref, "refs/tags/"):
			names = append(names, strings.TrimPrefix(ref, "refs/tags/"))
		}
	}
	sort.Strings(names)
	return names
}

// ResolveRef takes a user-supplied ref (e.g. "main", "v1.2.0", a SHA) and a
// map from `LsRemote` and returns the corresponding full SHA. If the input
// looks like a SHA already (40 lowercase hex chars), it's returned as-is.
// Resolution order matches git's own: exact ref → refs/heads/<ref> →
// refs/tags/<ref>. When the ref can't be found, a *RefNotFoundError carrying
// the available ref names is returned so callers can recover.
func ResolveRef(input string, refs map[string]string) (string, error) {
	if isHexSHA(input) {
		return input, nil
	}
	if sha, ok := refs[input]; ok {
		return sha, nil
	}
	candidates := []string{
		"refs/heads/" + input,
		"refs/tags/" + input,
		"refs/tags/" + input + "^{}", // peeled annotated tag
	}
	for _, c := range candidates {
		if sha, ok := refs[c]; ok {
			return sha, nil
		}
	}
	return "", &RefNotFoundError{Ref: input, Available: AvailableRefNames(refs)}
}

func isHexSHA(s string) bool {
	if len(s) != 40 && len(s) != 7 && len(s) != 8 && len(s) != 12 {
		// We accept full SHAs and a few common short forms. The actual
		// short-form is whatever git resolves; this is a fast pre-check
		// only.
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// CloneOptions controls a clone invocation.
type CloneOptions struct {
	URL    string
	Ref    string // tag, branch, or SHA. Empty means HEAD.
	Target string // destination directory. Must not exist.
	Depth  int    // 0 → shallow with depth 1
	// Subdir, when non-empty, restricts the working tree to this path (plus
	// repository-root files) via a partial (--filter=blob:none) sparse
	// checkout. Use when only a single module subdirectory is needed so we
	// don't download blobs for the entire repository.
	Subdir string
}

// Clone runs git clone --depth <D> [--branch <ref>] <url> <target>. For SHA
// refs we follow with `git fetch && git checkout <sha>` since `--branch`
// doesn't accept SHAs. When opts.Subdir is set, the clone is a partial,
// sparse checkout limited to that subdirectory.
func Clone(ctx context.Context, r Runner, opts CloneOptions) error {
	if opts.URL == "" || opts.Target == "" {
		return errors.New("clone: URL and Target are required")
	}
	if _, err := os.Stat(opts.Target); err == nil {
		return fmt.Errorf("clone: target %s already exists", opts.Target)
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 1
	}
	sparse := opts.Subdir != ""
	args := []string{"clone", fmt.Sprintf("--depth=%d", depth)}
	if sparse {
		// Partial clone: fetch trees but no blobs until checkout, and
		// initialize sparse-checkout (cone mode) so only root files are
		// materialized until we narrow to the subdir below.
		args = append(args, "--filter=blob:none", "--sparse")
	}
	if opts.Ref != "" && !isHexSHA(opts.Ref) {
		args = append(args, "--branch", opts.Ref)
	}
	args = append(args, opts.URL, opts.Target)
	_, stderr, err := r.Run(ctx, "", args...)
	if err != nil {
		return fmt.Errorf("git clone: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	if sparse {
		// Narrow the working tree (and on-demand blob fetch) to just the
		// module subdirectory.
		if _, stderr, err := r.Run(ctx, opts.Target, "sparse-checkout", "set", opts.Subdir); err != nil {
			return fmt.Errorf("git sparse-checkout set %s: %w (%s)", opts.Subdir, err, strings.TrimSpace(string(stderr)))
		}
	}
	if opts.Ref != "" && isHexSHA(opts.Ref) {
		// Fetch the specific SHA and check it out. Shallow clones don't
		// have arbitrary SHAs by default; we have to unshallow or fetch.
		if _, _, err := r.Run(ctx, opts.Target, "fetch", "--depth=1", "origin", opts.Ref); err != nil {
			return fmt.Errorf("git fetch %s: %w", opts.Ref, err)
		}
		if _, _, err := r.Run(ctx, opts.Target, "checkout", opts.Ref); err != nil {
			return fmt.Errorf("git checkout %s: %w", opts.Ref, err)
		}
	}
	return nil
}

// HeadSHA returns the SHA of HEAD in the given clone directory.
func HeadSHA(ctx context.Context, r Runner, dir string) (string, error) {
	stdout, stderr, err := r.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w (%s)", err, strings.TrimSpace(string(stderr)))
	}
	return strings.TrimSpace(string(stdout)), nil
}
