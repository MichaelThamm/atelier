// Command gallery-candidates lists product and solution Terraform modules in an
// org that are not yet in the bundled gallery, so a maintainer can triage
// additions on demand.
//
// It is a prompt, not a gate: it never edits the manifest and runs on no
// schedule. Only the mechanical ref bump is automated (ADR-0040); deciding that
// a module belongs in the gallery — its subdir, preset, and required inputs — is
// a human judgement, so discovery stays a tool a person runs.
//
// It queries GitHub code search for `variables.tf` files under a `terraform/`
// path, keeps the product/solution module roots, and subtracts the repo/subdir
// pairs the gallery already covers. A repo whose product module is already an
// entry is not suggested; a second module in an already-covered repo is.
//
//	GITHUB_TOKEN=... go run ./tools/gallerycandidates
//	go run ./tools/gallerycandidates -org canonical -all
//
// Needs a GitHub token that can read public repositories: set GITHUB_TOKEN (or
// GH_TOKEN), or be logged in with `gh auth login`. A run makes a handful of
// code-search requests, well within the API's 10-per-minute limit.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
)

const (
	codeSearchEndpoint  = "https://api.github.com/search/code"
	codeSearchPageSize  = 100
	codeSearchMaxPages  = 10 // GitHub caps code search at 1000 results
	codeSearchUserAgent = "atelier-gallery-candidates"
)

// codeResult is one code-search hit: the repository slug and the file path.
type codeResult struct {
	Repo string // owner/name
	Path string // path within the repo, e.g. terraform/product/variables.tf
}

// candidate is a module root the gallery does not cover.
type candidate struct {
	Repo string
	Dir  string
}

// searcher returns the code-search hits for a query. Injected so tests need no
// network and no token.
type searcher func(ctx context.Context, query string) ([]codeResult, error)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run reports the candidates and returns a process exit code. search may be nil,
// in which case a GitHub-backed searcher is built from the environment.
func run(args []string, stdout, stderr io.Writer, search searcher) int {
	fs := flag.NewFlagSet("gallery-candidates", flag.ContinueOnError)
	fs.SetOutput(stderr)
	org := fs.String("org", "canonical", "GitHub org to search")
	all := fs.Bool("all", false, "include every terraform root, not only product/solution modules")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if search == nil {
		token, err := githubToken()
		if err != nil {
			fmt.Fprintf(stderr, "gallery-candidates: %v\n", err)
			return 1
		}
		search = newSearcher(token)
	}

	entries, err := gallery.List()
	if err != nil {
		fmt.Fprintf(stderr, "gallery-candidates: %v\n", err)
		return 1
	}
	results, err := search(context.Background(), codeQuery(*org))
	if err != nil {
		fmt.Fprintf(stderr, "gallery-candidates: %v\n", err)
		return 1
	}

	uncovered := uncoveredCandidates(candidateModules(results, *all), entries)
	for _, c := range uncovered {
		fmt.Fprintf(stdout, "%s\t%s\thttps://github.com/%s\n", c.Repo, c.Dir, c.Repo)
	}
	if len(uncovered) == 0 {
		fmt.Fprintln(stderr, "no new candidates: every product/solution module is already in the gallery")
		return 0
	}
	fmt.Fprintf(stderr, "%d candidate(s) not in the gallery\n", len(uncovered))
	return 0
}

// codeQuery anchors the search on module roots: a Terraform module declares its
// variables in `variables.tf`, so a hit's directory is a candidate root.
func codeQuery(org string) string {
	return fmt.Sprintf("org:%s path:terraform filename:variables.tf", org)
}

// candidateModules derives the module roots from search hits, dropping
// duplicates and ordering for a stable report.
func candidateModules(results []codeResult, all bool) []candidate {
	seen := map[candidate]bool{}
	var out []candidate
	for _, r := range results {
		dir := path.Dir(r.Path)
		if !isModuleRootDir(dir, all) {
			continue
		}
		c := candidate{Repo: r.Repo, Dir: dir}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// isModuleRootDir reports whether dir looks like a product module root worth
// suggesting. `terraform/product`, `terraform/products/<name>`, and
// `terraform/solution` are the conventions the gallery entries use; -all widens
// this to any `terraform/` root. Child modules and test/example trees are never
// roots: an operator's `terraform/variables.tf` is a single-charm test module,
// and `terraform/product/modules/<x>` is a child of the product module.
func isModuleRootDir(dir string, all bool) bool {
	segs := strings.Split(dir, "/")
	if len(segs) == 0 || segs[0] != "terraform" {
		return false
	}
	for _, s := range segs {
		switch s {
		case "modules", "tests", "test", "examples", "example":
			return false
		}
	}
	if all {
		return true
	}
	if len(segs) < 2 {
		return false
	}
	switch segs[1] {
	case "product", "products", "solution":
		return true
	}
	return false
}

// uncoveredCandidates keeps the candidates no gallery entry already covers. An
// entry covers a candidate when they name the same repo and either subdir
// contains the other, so a second scenario under an entry's directory (and a
// repo added at a narrower subdir) is not re-reported.
func uncoveredCandidates(candidates []candidate, entries []gallery.Entry) []candidate {
	covered := map[string][]string{}
	for _, e := range entries {
		slug := repoSlug(e.Module)
		covered[slug] = append(covered[slug], e.Subdir)
	}
	var out []candidate
	for _, c := range candidates {
		if coversAny(covered[c.Repo], c.Dir) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func coversAny(subdirs []string, dir string) bool {
	for _, s := range subdirs {
		if s == dir || strings.HasPrefix(dir, s+"/") || strings.HasPrefix(s, dir+"/") {
			return true
		}
	}
	return false
}

// repoSlug reduces a gallery module URL to its `owner/name` slug, matching the
// `full_name` code search reports.
func repoSlug(module string) string {
	m := strings.TrimSuffix(module, ".git")
	m = strings.TrimPrefix(m, "https://github.com/")
	m = strings.TrimPrefix(m, "http://github.com/")
	return strings.TrimSuffix(m, "/")
}

// githubToken resolves a token from the environment, falling back to the `gh`
// CLI so a local run works without exporting anything.
func githubToken() (string, error) {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v, nil
		}
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
		if t := strings.TrimSpace(string(out)); t != "" {
			return t, nil
		}
	}
	return "", fmt.Errorf("no GitHub token: set GITHUB_TOKEN (or GH_TOKEN), or run `gh auth login`")
}

// searchClient pages through GitHub's code search API.
type searchClient struct {
	token string
	http  *http.Client
}

func newSearcher(token string) searcher {
	c := &searchClient{token: token, http: &http.Client{}}
	return c.search
}

// search returns every hit for query, up to the API's 1000-result cap.
func (c *searchClient) search(ctx context.Context, query string) ([]codeResult, error) {
	var out []codeResult
	for page := 1; page <= codeSearchMaxPages; page++ {
		items, total, err := c.page(ctx, query, page)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			out = append(out, codeResult{Repo: it.Repository.FullName, Path: it.Path})
		}
		if len(items) < codeSearchPageSize || page*codeSearchPageSize >= total {
			break
		}
	}
	return out, nil
}

// codeItem mirrors the fields of one code-search result.
type codeItem struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Path string `json:"path"`
}

func (c *searchClient) page(ctx context.Context, query string, page int) ([]codeItem, int, error) {
	u, err := url.Parse(codeSearchEndpoint)
	if err != nil {
		return nil, 0, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("per_page", strconv.Itoa(codeSearchPageSize))
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", codeSearchUserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("github code search: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		TotalCount int        `json:"total_count"`
		Items      []codeItem `json:"items"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("github code search: decode response: %w", err)
	}
	return parsed.Items, parsed.TotalCount, nil
}
