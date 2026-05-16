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

// ResolveRef takes a user-supplied ref (e.g. "main", "v1.2.0", a SHA) and a
// map from `LsRemote` and returns the corresponding full SHA. If the input
// looks like a SHA already (40 lowercase hex chars), it's returned as-is.
// Resolution order matches git's own: exact ref → refs/heads/<ref> →
// refs/tags/<ref>.
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
	return "", fmt.Errorf("ref %q not found among %d remote refs", input, len(refs))
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
}

// Clone runs git clone --depth <D> [--branch <ref>] <url> <target>. For SHA
// refs we follow with `git fetch && git checkout <sha>` since `--branch`
// doesn't accept SHAs.
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
	args := []string{"clone", fmt.Sprintf("--depth=%d", depth)}
	if opts.Ref != "" && !isHexSHA(opts.Ref) {
		args = append(args, "--branch", opts.Ref)
	}
	args = append(args, opts.URL, opts.Target)
	_, stderr, err := r.Run(ctx, "", args...)
	if err != nil {
		return fmt.Errorf("git clone: %w (%s)", err, strings.TrimSpace(string(stderr)))
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
