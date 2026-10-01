// Package modulesource parses Terraform module source addresses.
//
// A Terraform git module source encodes three things in one string:
//
//	git::https://github.com/org/repo.git//terraform/cos-lite?ref=v1.2.0
//	└─┬─┘ └──────────────┬────────────┘ └────────┬────────┘ └──┬──┘
//	prefix          remote URL              module path      ref
//
// Before this package existed that surgery was copied into bootstrap, convert,
// and cmd/atelier with subtly different edge-case handling. It lives here once
// so the CLI, the wrapper bootstrap, and the ref switcher agree on what a
// source means. The package is a leaf: it depends only on the standard library.
package modulesource

import "strings"

const (
	gitPrefix = "git::"
	refQuery  = "?ref="
)

// Decompose splits a module source into its remote URL and ref, discarding the
// //subdir module path. The git:: prefix and ?ref= query are removed. It is the
// inverse of Compose.
func Decompose(source string) (remote, ref string) {
	remote = source
	if i := strings.Index(remote, refQuery); i >= 0 {
		ref = remote[i+len(refQuery):]
		remote = remote[:i]
	}
	remote = strings.TrimPrefix(remote, gitPrefix)
	// Strip the //<modulepath> suffix, skipping the scheme's :// so it is not
	// mistaken for the separator.
	search := remote
	offset := 0
	if i := strings.Index(search, "://"); i >= 0 {
		offset = i + 3
		search = remote[offset:]
	}
	if j := strings.Index(search, "//"); j >= 0 {
		remote = remote[:offset+j]
	}
	return remote, ref
}

// Remote returns just the remote URL component of a module source, discarding
// the ref and //subdir path. It is a convenience over Decompose for callers
// that only need the URL.
func Remote(source string) string {
	remote, _ := Decompose(source)
	return remote
}

// ModulePath returns the //subdir component of a module source, or "" when the
// source points at the repository root.
func ModulePath(source string) string {
	s := source
	if i := strings.Index(s, refQuery); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, gitPrefix)
	searchFrom := 0
	if schemeEnd := strings.Index(s, "://"); schemeEnd >= 0 {
		searchFrom = schemeEnd + 3
	}
	if idx := strings.Index(s[searchFrom:], "//"); idx >= 0 {
		return s[searchFrom+idx+2:]
	}
	return ""
}

// Compose builds a canonical Terraform module source from a remote URL, module
// sub-path, and ref. Local paths are left without a git:: prefix; anything else
// is treated as a git remote and gets one.
func Compose(remote, modulePath, ref string) string {
	url := remote
	if !strings.HasPrefix(url, gitPrefix) &&
		!strings.HasPrefix(url, "./") &&
		!strings.HasPrefix(url, "../") &&
		!strings.HasPrefix(url, "/") {
		url = gitPrefix + url
	}
	if modulePath != "" && modulePath != "." {
		url += "//" + modulePath
	}
	if ref != "" {
		url += refQuery + ref
	}
	return url
}

// RepoBasename pulls a directory name from a source: the repository name for a
// git remote, or the directory basename for a local path. Used to derive a
// module block label when the module sub-path is uninformative.
func RepoBasename(source string) string {
	s := strings.TrimPrefix(source, gitPrefix)
	if i := strings.Index(s, "?"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimSuffix(s, "/")
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		s = s[i+1:]
	}
	if s == "" || s == "." || s == ".." {
		s = "repo"
	}
	return s
}

// IsLocal reports whether source is a local filesystem path rather than a git
// remote.
func IsLocal(source string) bool {
	return strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, "/")
}

// IsGitSource reports whether source looks like a git remote rather than a
// local path or a registry source. It is intentionally looser than !IsLocal:
// it also rejects registry references such as "hashicorp/consul/aws".
func IsGitSource(source string) bool {
	if strings.HasPrefix(source, gitPrefix) {
		return true
	}
	if strings.HasPrefix(source, "github.com/") {
		return true
	}
	if strings.Contains(source, "://") && !strings.HasPrefix(source, "file://") {
		return true
	}
	return false
}

// IsFullSHA reports whether s is a full 40-character lowercase hex commit SHA.
// It deliberately does not accept git's short forms: callers use it to decide
// whether a module's pinned ref is an immutable commit. gitops keeps a
// separate, looser check for refs git itself resolves.
func IsFullSHA(s string) bool {
	if len(s) != 40 {
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
