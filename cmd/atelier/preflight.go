package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// errNotInteractive is returned by confirm when there is no terminal to prompt
// on. Callers surface it as an error rather than proceeding: a destructive or
// directory-cluttering action must not happen just because nobody was there to
// say no. The message names the escape hatch.
var errNotInteractive = errors.New("cannot prompt for confirmation: stdin is not a terminal (re-run with --yes to proceed without prompting)")

// confirm asks a yes/no question on the terminal and reports the answer.
//
// This is the single confirmation path for the whole CLI. Before it existed,
// `purge` and `module rm` each had their own: different readers, different
// streams, different accepted answers, and neither checked for a terminal — so
// in CI both blocked on a closed stdin instead of failing with a usable message.
//
// The prompt goes to stderr so that piping a command's stdout somewhere never
// swallows the question.
func confirm(prompt string) (bool, error) {
	if !isTerminal(os.Stdin) {
		return false, errNotInteractive
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && answer == "" {
		return false, nil
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}

// concernLevel ranks a preflight finding.
type concernLevel int

const (
	// levelNote is context worth printing but never worth prompting over.
	levelNote concernLevel = iota
	// levelWarn means the directory is not what Atelier normally expects.
	levelWarn
	// levelAlarm means the directory looks like somewhere the user almost
	// certainly did not mean to scaffold into.
	levelAlarm
)

// concern is one preflight finding about a target directory.
type concern struct {
	level  concernLevel
	detail string
	// hint is an optional second line suggesting what to do instead.
	hint string
}

// benignEntries are directory entries that do not count as clutter: Atelier
// either authors them itself or they are the ordinary furniture of an empty-ish
// project directory. Warning about these would make the prompt fire so often
// that users would learn to dismiss it unread, which is worse than not having
// it.
var benignEntries = map[string]bool{
	".git":                     true,
	".gitignore":               true,
	".gitattributes":           true,
	".DS_Store":                true,
	".atelier":                 true,
	".clone":                   true,
	".terraform":               true,
	".terraform.lock.hcl":      true,
	"readme.md":                true,
	"license":                  true,
	"license.md":               true,
	"license.txt":              true,
	"licence":                  true,
	"copying":                  true,
	"notice":                   true,
	"terraform.tfstate":        true,
	"terraform.tfstate.backup": true,
}

// foreignProjectMarkers name files that identify a directory as the root of a
// project of some other kind. Scaffolding a Terraform wrapper into a Go module
// or an npm package is nearly always a mistake — the user meant to be in a
// subdirectory.
var foreignProjectMarkers = []string{
	"go.mod",
	"package.json",
	"pyproject.toml",
	"setup.py",
	"Cargo.toml",
	"pom.xml",
	"build.gradle",
	"Gemfile",
	"composer.json",
	"snapcraft.yaml",
	"charmcraft.yaml",
	"rockcraft.yaml",
}

// inspectTarget reports everything questionable about using dir as a wrapper
// directory. An empty result means the directory is unremarkable and the caller
// should proceed without a prompt.
//
// Every check is advisory and best-effort: a filesystem error is treated as
// "nothing to report" rather than failing the command, because the checks exist
// to add a prompt, not to add a new way for `module add` to refuse to run.
func inspectTarget(dir string) []concern {
	var out []concern

	if c, ok := specialDirConcern(dir); ok {
		out = append(out, c)
	}
	if c, ok := nestedWrapperConcern(dir); ok {
		out = append(out, c)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}

	// Foreign project markers.
	present := map[string]bool{}
	for _, e := range entries {
		present[e.Name()] = true
	}
	var markers []string
	for _, m := range foreignProjectMarkers {
		if present[m] {
			markers = append(markers, m)
		}
	}
	if len(markers) > 0 {
		out = append(out, concern{
			level:  levelAlarm,
			detail: fmt.Sprintf("this looks like the root of another project (%s)", strings.Join(markers, ", ")),
			hint:   "Atelier writes main.tf, versions.tf, providers.tf and .atelier/ here; a dedicated subdirectory is usually what you want.",
		})
	}

	// Existing Terraform, and the specific collisions it causes.
	out = append(out, terraformConcerns(dir, entries)...)

	// Anything else that would simply be cluttered.
	var clutter []string
	for _, e := range entries {
		name := e.Name()
		if benignEntries[strings.ToLower(name)] {
			continue
		}
		if strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tfstate") {
			continue // already covered by terraformConcerns
		}
		clutter = append(clutter, name)
	}
	if len(clutter) > 0 {
		sort.Strings(clutter)
		out = append(out, concern{
			level:  levelWarn,
			detail: fmt.Sprintf("directory is not empty (%s)", summariseEntries(clutter)),
		})
	}

	return out
}

// terraformConcerns reports findings about Terraform files already in dir: both
// the general "this is somebody's Terraform root" signal and the specific
// declaration collisions that would make the bootstrapped root fail to
// initialise.
func terraformConcerns(dir string, entries []os.DirEntry) []concern {
	var tfFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			tfFiles = append(tfFiles, e.Name())
		}
	}
	if len(tfFiles) == 0 {
		return nil
	}
	sort.Strings(tfFiles)

	decls, err := wrapper.ScanDeclarations(dir)
	if err != nil {
		return nil
	}

	var out []concern
	if decls.LooksHandAuthored() {
		what := "existing Terraform configuration"
		if len(decls.OtherBlockTypes) > 0 {
			what = fmt.Sprintf("hand-authored Terraform (%s blocks)", strings.Join(decls.OtherBlockTypes, ", "))
		}
		out = append(out, concern{
			level:  levelAlarm,
			detail: fmt.Sprintf("%s already lives here (%s)", what, summariseEntries(tfFiles)),
			hint:   "Atelier will add a module block to main.tf and treat this directory as a wrapper. Your own blocks are preserved, but Atelier rewrites main.tf on every save.",
		})
	} else {
		out = append(out, concern{
			level:  levelWarn,
			detail: fmt.Sprintf("directory already contains Terraform files (%s)", summariseEntries(tfFiles)),
		})
	}

	if decls.HasRequiredProvidersBlock() {
		out = append(out, concern{
			level:  levelNote,
			detail: fmt.Sprintf("%s already declares required_providers, so Atelier will not write versions.tf; you may need to add the module's provider requirements there", decls.RequiredProvidersFile),
		})
	}
	if len(decls.Providers) > 0 {
		names := make([]string, 0, len(decls.Providers))
		for n := range decls.Providers {
			names = append(names, n)
		}
		sort.Strings(names)
		out = append(out, concern{
			level:  levelNote,
			detail: fmt.Sprintf("your provider configuration for %s will be kept as-is", strings.Join(names, ", ")),
		})
	}
	return out
}

// specialDirConcern flags directories that are somebody's home, a config root,
// or the filesystem root — i.e. a shell that wandered rather than a project.
func specialDirConcern(dir string) (concern, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return concern{}, false
	}
	clean := filepath.Clean(abs)

	if clean == string(filepath.Separator) {
		return concern{
			level:  levelAlarm,
			detail: "target is the filesystem root",
		}, true
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if clean == filepath.Clean(home) {
			return concern{
				level:  levelAlarm,
				detail: "target is your home directory",
				hint:   "create and enter a directory for the wrapper first, e.g. `mkdir cos-lite && cd cos-lite`.",
			}, true
		}
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" && strings.HasPrefix(clean+string(filepath.Separator), filepath.Clean(cfg)+string(filepath.Separator)) {
			return concern{
				level:  levelAlarm,
				detail: fmt.Sprintf("target is inside your config directory (%s)", cfg),
			}, true
		}
	}
	return concern{}, false
}

// nestedWrapperMaxDepth bounds the parent walk. A wrapper more than a few
// levels up is unrelated to what the user is doing here, and an unbounded walk
// on a deep path costs a stat per level for no benefit.
const nestedWrapperMaxDepth = 8

// nestedWrapperConcern flags a target that sits inside an existing wrapper.
// Nesting produces two wrappers whose clones and Terraform state overlap in
// ways neither Atelier nor Terraform will make sense of.
func nestedWrapperConcern(dir string) (concern, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return concern{}, false
	}
	cur := filepath.Clean(abs)
	for i := 0; i < nestedWrapperMaxDepth; i++ {
		parent := filepath.Dir(cur)
		if parent == cur {
			return concern{}, false
		}
		cur = parent
		if isWrapperDir(cur) {
			return concern{
				level:  levelAlarm,
				detail: fmt.Sprintf("this directory is inside an existing wrapper (%s)", cur),
				hint:   "nested wrappers share nothing and confuse Terraform state; add the module to the outer wrapper instead.",
			}, true
		}
		// Stop at a repository boundary: crossing it would report a wrapper
		// that has nothing to do with this checkout.
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return concern{}, false
		}
	}
	return concern{}, false
}

// isWrapperDir reports whether dir is an Atelier wrapper: a main.tf plus
// Atelier's own state directory.
func isWrapperDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, wrapper.MainTF)); err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, wrapper.AtelierDir))
	return err == nil && info.IsDir()
}

// summariseEntries renders a bounded, comma-separated list so a prompt about a
// directory holding 400 files stays one line long.
func summariseEntries(names []string) string {
	const max = 5
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:max], ", "), len(names)-max)
}

// confirmTargetDir runs the preflight checks on dir and, if anything warrants
// it, prints the findings and asks the user to confirm.
//
// action names what is about to happen ("bootstrap a wrapper in"), so the
// question reads as a sentence. yes skips the prompt entirely, for scripts and
// CI. Notes are always printed when they exist, because they describe writes
// Atelier is about to skip.
//
// Returns false when the user declined; the caller should return nil (an
// abort at the user's request is not an error).
func confirmTargetDir(dir, action string, yes bool) (bool, error) {
	concerns := inspectTarget(dir)
	if len(concerns) == 0 {
		return true, nil
	}

	needsPrompt := false
	for _, c := range concerns {
		if c.level >= levelWarn {
			needsPrompt = true
			break
		}
	}

	printConcerns(dir, concerns)
	if !needsPrompt || yes {
		return true, nil
	}

	ok, err := confirm(fmt.Sprintf("%s %s?", action, dir))
	if err != nil {
		return false, err
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "aborted")
	}
	return ok, nil
}

func printConcerns(dir string, concerns []concern) {
	fmt.Fprintf(os.Stderr, "%s\n", dir)
	for _, c := range concerns {
		prefix := "note:"
		if c.level >= levelWarn {
			prefix = "warning:"
		}
		fmt.Fprintf(os.Stderr, "  %s %s\n", prefix, c.detail)
		if c.hint != "" {
			fmt.Fprintf(os.Stderr, "    %s\n", c.hint)
		}
	}
}
