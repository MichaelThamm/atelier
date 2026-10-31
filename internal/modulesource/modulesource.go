// Package modulesource parses Terraform module source addresses.
//
// A git source encodes three things in one string:
//
//	git::https://github.com/org/repo.git//terraform/cos-lite?ref=v1.2.0
//	└─┬─┘ └──────────────┬────────────┘ └────────┬────────┘ └──┬──┘
//	prefix          remote URL              module path      ref
//
// A git source may also carry a shallow-clone depth as an extra query
// parameter, after the ref:
//
//	git::https://github.com/org/repo.git//terraform/cos-lite?ref=v1.2.0&depth=1
//
// The package is a leaf: it depends only on the standard library.
package modulesource

import (
	"strconv"
	"strings"
)

const (
	gitPrefix = "git::"
	refQuery  = "?ref="
)

// Decompose splits a module source into its remote URL and ref, discarding the
// //subdir module path and any other query parameter (such as depth). The
// git:: prefix and ?ref= query are removed. It is the inverse of Compose.
func Decompose(source string) (remote, ref string) {
	remote = source
	if i := strings.Index(remote, "?"); i >= 0 {
		ref = queryValue(remote[i+1:], "ref")
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
	if i := strings.Index(s, "?"); i >= 0 {
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
// sub-path, ref, and shallow-clone depth. Local paths are left without a git::
// prefix; anything else is treated as a git remote and gets one. depth <= 0
// omits the parameter; a positive depth is written only when EffectiveDepth
// allows it for the ref.
func Compose(remote, modulePath, ref string, depth int) string {
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
	query := ""
	if ref != "" {
		query = refQuery + ref
	}
	// A local path is not a git remote, so it has nothing to clone shallow.
	if !IsLocal(remote) {
		if d := EffectiveDepth(depth, ref); d > 0 {
			if query == "" {
				query = "?depth=" + strconv.Itoa(d)
			} else {
				query += "&depth=" + strconv.Itoa(d)
			}
		}
	}
	return url + query
}

// EffectiveDepth reports the shallow-clone depth a module source may carry for
// a given ref. Terraform rejects the depth parameter unless ref is a branch or
// tag name, so a full commit SHA forces a full clone (depth 0), as does any
// non-positive depth.
func EffectiveDepth(depth int, ref string) int {
	if depth <= 0 {
		return 0
	}
	if ref != "" && IsFullSHA(ref) {
		return 0
	}
	return depth
}

// Depth returns the shallow-clone depth declared in a module source's query
// string, and whether one is present. A missing, non-numeric, or non-positive
// depth reads as absent.
func Depth(source string) (int, bool) {
	i := strings.Index(source, "?")
	if i < 0 {
		return 0, false
	}
	for _, kv := range strings.Split(source[i+1:], "&") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k != "depth" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// DepthOf returns the shallow-clone depth declared in a module source, or 0
// when it carries none.
func DepthOf(source string) int {
	if d, ok := Depth(source); ok {
		return d
	}
	return 0
}

// queryValue returns the value of key in a URL query string, or "" when absent.
func queryValue(query, key string) string {
	for _, kv := range strings.Split(query, "&") {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k == key {
			return v
		}
	}
	return ""
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
