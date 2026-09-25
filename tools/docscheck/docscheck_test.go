package docscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	adrRefRe   = regexp.MustCompile(`ADR-(\d{4})`)
	adrFileRe  = regexp.MustCompile(`^(\d{4})-`)
	indexRowRe = regexp.MustCompile(`^\|\s*(\d{4})\s*\|\s*\[[^\]]+\]\(([^)]+)\)\s*\|\s*([^|]+?)\s*\|`)
	mdLinkRe   = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
)

// repoRoot walks up from the test's working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (no go.mod found upward)")
		}
		dir = parent
	}
}

// trackedFiles lists tracked files matching pattern with `git ls-files`. It
// skips the test when git is unavailable so the package stays runnable in a
// bare source checkout.
func trackedFiles(t *testing.T, root, pattern string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "--", pattern)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git ls-files unavailable: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// adrStatusLine returns the first non-empty line after the ADR's `## Status`
// heading, or "" when the heading is absent.
func adrStatusLine(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "## Status" {
			continue
		}
		for _, next := range lines[i+1:] {
			if s := strings.TrimSpace(next); s != "" {
				return s
			}
		}
	}
	return ""
}

// canonicalStatus reduces a status line to the value the index uses. ADR
// bodies are allowed richer sentences ("Accepted (updated: …)") than the
// index; only the leading status word is contractual.
func canonicalStatus(status string) string {
	s := strings.TrimSpace(status)
	switch {
	case strings.HasPrefix(s, "Superseded"):
		if m := adrRefRe.FindStringSubmatch(s); m != nil {
			return "Superseded by ADR-" + m[1]
		}
		return "Superseded"
	case strings.HasPrefix(s, "Deprecated"):
		return "Deprecated"
	case strings.HasPrefix(s, "Proposed"):
		return "Proposed"
	case strings.HasPrefix(s, "Accepted"):
		return "Accepted"
	}
	return s
}

func TestADRIndexMatchesFiles(t *testing.T) {
	root := repoRoot(t)

	filesByNum := map[string]string{}
	for _, f := range trackedFiles(t, root, "docs/adr/*.md") {
		base := filepath.Base(f)
		if base == "README.md" {
			continue
		}
		m := adrFileRe.FindStringSubmatch(base)
		if m == nil {
			t.Errorf("ADR file %s is not NNNN-slug.md", f)
			continue
		}
		if prev, ok := filesByNum[m[1]]; ok {
			t.Errorf("duplicate ADR number %s: %s and %s", m[1], prev, f)
		}
		filesByNum[m[1]] = f
	}
	if len(filesByNum) == 0 {
		t.Fatal("no ADR files found; is the repo layout correct?")
	}

	data, err := os.ReadFile(filepath.Join(root, "docs/adr/README.md"))
	if err != nil {
		t.Fatal(err)
	}

	indexed := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		m := indexRowRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		num, link, indexStatus := m[1], m[2], strings.TrimSpace(m[3])
		if indexed[num] {
			t.Errorf("docs/adr/README.md has more than one row for ADR-%s", num)
		}
		indexed[num] = true

		if !adrFileRe.MatchString(filepath.Base(link)) || filepath.Base(link)[:4] != num {
			t.Errorf("index row ADR-%s links to %s, whose number does not match", num, link)
		}
		abs := filepath.Join(root, "docs/adr", link)
		body, err := os.ReadFile(abs)
		if err != nil {
			t.Errorf("index row ADR-%s links to missing file %s", num, link)
			continue
		}
		if got := canonicalStatus(adrStatusLine(string(body))); got != indexStatus {
			t.Errorf("ADR-%s status drift: index says %q, body says %q", num, indexStatus, got)
		}
	}

	nums := make([]string, 0, len(filesByNum))
	for num := range filesByNum {
		nums = append(nums, num)
	}
	sort.Strings(nums)
	for _, num := range nums {
		if !indexed[num] {
			t.Errorf("ADR-%s (%s) has no row in docs/adr/README.md", num, filesByNum[num])
		}
	}
}

func TestADRReferencesResolve(t *testing.T) {
	root := repoRoot(t)

	known := map[string]bool{}
	for _, f := range trackedFiles(t, root, "docs/adr/*.md") {
		base := filepath.Base(f)
		if base == "README.md" {
			continue
		}
		if m := adrFileRe.FindStringSubmatch(base); m != nil {
			known[m[1]] = true
		}
	}
	if len(known) == 0 {
		t.Fatal("no ADR files found; cannot validate references")
	}

	for _, pattern := range []string{"*.md", "*.go"} {
		for _, f := range trackedFiles(t, root, pattern) {
			data, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range adrRefRe.FindAllStringSubmatch(string(data), -1) {
				if !known[m[1]] {
					t.Errorf("%s references ADR-%s, which does not exist", f, m[1])
				}
			}
		}
	}
}

func TestMarkdownLinksResolve(t *testing.T) {
	root := repoRoot(t)

	for _, f := range trackedFiles(t, root, "*.md") {
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Dir(filepath.Join(root, f))
		for _, m := range mdLinkRe.FindAllStringSubmatch(string(data), -1) {
			target := cleanLinkTarget(m[1])
			if target == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
				t.Errorf("%s: relative link %q does not resolve", f, m[1])
			}
		}
	}
}

// cleanLinkTarget normalizes an inline-link target and returns "" for targets
// that are not relative filesystem paths (external URLs, anchors, or absolute
// paths).
func cleanLinkTarget(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "<") {
		// <path with spaces> — the angle brackets allow spaces in the path.
		if end := strings.IndexByte(s, '>'); end >= 0 {
			s = s[1:end]
		} else {
			s = s[1:]
		}
	} else if i := strings.IndexAny(s, " \t"); i >= 0 {
		// Optional link title: [text](path "title").
		s = s[:i]
	}
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "/") {
		return ""
	}
	for _, scheme := range []string{"http://", "https://", "mailto:", "tel:"} {
		if strings.HasPrefix(s, scheme) {
			return ""
		}
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return s
}
