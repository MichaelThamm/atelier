// Command gallery-bump moves each gallery entry's pinned `ref` to the current
// HEAD of its module, so a scheduled CI run can propose the bump as a pull
// request instead of waiting for a human to notice a moved pin.
//
// It only ever rewrites `ref`, and it does so by patching the manifest source
// rather than re-marshalling it, so a run's diff is exactly the SHA lines and
// nothing else. Module URL, subdir, block, presets, and requires are maintained
// by hand: a bump that breaks an entry must fail the gallery check and be fixed
// in that PR, not silently "fixed" here (ADR-0040).
//
//	go run ./tools/gallerybump            # rewrite the manifest in place
//	go run ./tools/gallerybump -dry-run   # report what would change
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/MichaelThamm/atelier/internal/gallery"
	"github.com/MichaelThamm/atelier/internal/gitops"
)

const defaultManifest = "internal/gallery/gallery.json"

// refResolver reports the commit an entry's module should be pinned to.
type refResolver func(module string) (string, error)

// manifest field lines. The tool patches the source rather than re-encoding it,
// so it must find the ref belonging to the entry whose "name" was seen last;
// JSON field order in the committed file puts name before ref.
var (
	nameLine = regexp.MustCompile(`^(\s*)"name":\s*"([^"]*)",?\s*$`)
	refLine  = regexp.MustCompile(`^(\s*)"ref":\s*"([^"]*)",?\s*$`)
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run bumps every entry and rewrites the manifest. resolve may be nil, in which
// case git is used to resolve each module's target ref. It exits non-zero when
// any module could not be reached, so a broken scheduled run is visible instead
// of quietly bumping the other entries.
func run(args []string, stdout, stderr io.Writer, resolve refResolver) int {
	fs := flag.NewFlagSet("gallery-bump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	manifestPath := fs.String("manifest", defaultManifest, "path to gallery.json")
	dryRun := fs.Bool("dry-run", false, "report the bumps without writing the manifest")
	targetRef := fs.String("ref", "HEAD", "ref to resolve in each module (HEAD, a branch, or a tag)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if resolve == nil {
		resolve = gitResolver(*targetRef)
	}

	src, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "gallery-bump: %v\n", err)
		return 1
	}
	entries, err := readManifest(src)
	if err != nil {
		fmt.Fprintf(stderr, "gallery-bump: %s: %v\n", *manifestPath, err)
		return 1
	}

	changes, failures := bump(entries, resolve)
	for _, c := range changes {
		fmt.Fprintln(stdout, c)
	}
	if failures > 0 {
		fmt.Fprintf(stderr, "gallery-bump: %d entr(ies) could not be resolved; pins left unchanged\n", failures)
		return 1
	}
	if len(changes) == 0 {
		fmt.Fprintf(stdout, "no bumps: every entry is already at its module's target ref\n")
		return 0
	}

	patched, err := rewriteRefs(src, entries)
	if err != nil {
		fmt.Fprintf(stderr, "gallery-bump: %s: %v\n", *manifestPath, err)
		return 1
	}
	if *dryRun {
		fmt.Fprintf(stdout, "%d bump(s) pending (dry run; manifest not written)\n", len(changes))
		return 0
	}
	if err := os.WriteFile(*manifestPath, patched, 0o644); err != nil {
		fmt.Fprintf(stderr, "gallery-bump: write %s: %v\n", *manifestPath, err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %d bump(s) to %s\n", len(changes), *manifestPath)
	return 0
}

// bump rewrites each entry's ref in place and returns one line per change: the
// bump for a moved entry, or the reason a module could not be resolved. An
// entry that fails keeps its pin — a half-bumped manifest is worse than none.
func bump(entries []gallery.Entry, resolve refResolver) (changes []string, failures int) {
	for i := range entries {
		sha, err := resolve(entries[i].Module)
		if err != nil {
			failures++
			changes = append(changes, fmt.Sprintf("%s: unresolved (%v), pin kept", entries[i].Name, err))
			continue
		}
		if sha == "" {
			failures++
			changes = append(changes, fmt.Sprintf("%s: %s resolved to an empty SHA, pin kept", entries[i].Name, entries[i].Module))
			continue
		}
		if sha == entries[i].Ref {
			continue
		}
		changes = append(changes, fmt.Sprintf("%s: %s -> %s", entries[i].Name, shortSHA(entries[i].Ref), sha))
		entries[i].Ref = sha
	}
	return changes, failures
}

// rewriteRefs patches each entry's ref line in the manifest source, leaving
// every other byte alone. It reports an error if the source does not contain
// one ref line per entry, so a reformatted manifest fails loudly instead of
// producing a half-patched file.
func rewriteRefs(src []byte, entries []gallery.Entry) ([]byte, error) {
	byName := make(map[string]string, len(entries))
	for _, e := range entries {
		byName[e.Name] = e.Ref
	}
	lines := strings.Split(string(src), "\n")
	current := ""
	patched := 0
	for i, line := range lines {
		if m := nameLine.FindStringSubmatch(line); m != nil {
			if _, ok := byName[m[2]]; ok {
				current = m[2]
			}
			continue
		}
		m := refLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		want, ok := byName[current]
		if !ok {
			return nil, fmt.Errorf("ref line %d (%q) has no preceding entry name", i+1, m[2])
		}
		if m[2] != want {
			lines[i] = fmt.Sprintf(`%s"ref": %q,`, m[1], want)
		}
		current = ""
		patched++
	}
	if patched != len(entries) {
		return nil, fmt.Errorf("patched %d ref line(s) for %d entry/entries", patched, len(entries))
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// gitResolver resolves a named ref (HEAD, a branch, or a tag) in a module via
// git ls-remote. An empty resolution is an error: pinning an entry to "" would
// produce a manifest that cannot clone.
func gitResolver(refName string) refResolver {
	return func(module string) (string, error) {
		refs, err := gitops.LsRemote(context.Background(), &gitops.Git{}, module)
		if err != nil {
			return "", err
		}
		if refName == "HEAD" {
			sha, ok := refs["HEAD"]
			if !ok || sha == "" {
				return "", fmt.Errorf("git ls-remote %s reported no HEAD", module)
			}
			return sha, nil
		}
		return gitops.ResolveRef(refName, refs)
	}
}

func readManifest(src []byte) ([]gallery.Entry, error) {
	var entries []gallery.Entry
	if err := json.Unmarshal(src, &entries); err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("manifest has no entries")
	}
	return entries, nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
