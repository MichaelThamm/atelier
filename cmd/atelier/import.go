package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// runImport implements `atelier import [PROVIDER] [flags]`.
//
// Two modes:
//
//  1. With --source: clone a remote module, write an Atelier wrapper, and
//     import live resources into it. The user's workflow is:
//     atelier import juju --source github.com/org/repo --var model_uuid=...
//
//  2. Without --source: import into an already-initialised Terraform root
//     (the directory must contain a provider and resource declarations).
//
// In both modes, `terraform query` discovers live objects, `terraform plan`
// finds the module's import candidates, and `terraform import` imports each
// matched resource by name.
//
// PROVIDER is an optional positional naming the provider to import from:
//
//	atelier import juju            # bare name -> source "juju/juju"
//	atelier import hashicorp/aws   # a value containing "/" is used verbatim
//
// When --source is given, PROVIDER is still accepted for the query (which
// list-resource types to discover). When --source is omitted, PROVIDER is
// also used to scaffold provider config if the directory has none.
func runImport(args []string) error {
	var (
		providerArg string
		dirArg      string
		sourceArg   string
		moduleArg   string
		refArg      string
		types       []string
		provVersion string
		noInit      bool
		strict      bool
		verbose     bool
		listOnly    bool
		config      = map[string]string{}
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--list":
			listOnly = true
		case a == "--no-init":
			noInit = true
		case a == "--strict":
			strict = true
		case a == "--verbose":
			verbose = true
		case a == "--source" || a == "--module" || a == "--ref" || a == "--type" || a == "--var" || a == "--dir" || a == "--provider-version":
			if i+1 >= len(args) {
				return fmt.Errorf("flag %q requires a value", a)
			}
			i++
			val := args[i]
			switch a {
			case "--source":
				sourceArg = val
			case "--module":
				moduleArg = val
			case "--ref":
				refArg = val
			case "--type":
				types = append(types, val)
			case "--dir":
				dirArg = val
			case "--provider-version":
				provVersion = val
			case "--var":
				k, v, ok := strings.Cut(val, "=")
				if !ok || k == "" {
					return fmt.Errorf("--var expects KEY=VALUE, got %q", val)
				}
				if err := validateVarKey(k); err != nil {
					return err
				}
				config[k] = v
			}
		case strings.HasPrefix(a, "--source="):
			sourceArg = strings.TrimPrefix(a, "--source=")
		case strings.HasPrefix(a, "--module="):
			moduleArg = strings.TrimPrefix(a, "--module=")
		case strings.HasPrefix(a, "--ref="):
			refArg = strings.TrimPrefix(a, "--ref=")
		case strings.HasPrefix(a, "--type="):
			types = append(types, strings.TrimPrefix(a, "--type="))
		case strings.HasPrefix(a, "--dir="):
			dirArg = strings.TrimPrefix(a, "--dir=")
		case strings.HasPrefix(a, "--provider-version="):
			provVersion = strings.TrimPrefix(a, "--provider-version=")
		case strings.HasPrefix(a, "--var="):
			kv := strings.TrimPrefix(a, "--var=")
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("--var expects KEY=VALUE, got %q", kv)
			}
			if err := validateVarKey(k); err != nil {
				return err
			}
			config[k] = v
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q for import", a)
		default:
			if providerArg != "" {
				return fmt.Errorf("import accepts at most one PROVIDER argument (use --dir or --source for the module)")
			}
			providerArg = a
		}
	}

	dir := dirArg
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		dir = cwd
	} else {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		dir = abs
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("directory does not exist: %s", dir)
	}

	// When --source is given, clone the module and write an Atelier wrapper
	// so the directory has a proper Terraform root to import into.
	var wrapperState *wrapper.State
	if sourceArg != "" {
		var err error
		dir, wrapperState, err = setupSourceModule(dir, sourceArg, moduleArg, refArg)
		if err != nil {
			return err
		}
	}

	provider := resolveProviderSource(providerArg)

	if provider == "" && !importer.HasProviderConfig(dir) {
		provider = resolveProviderSource(promptProvider())
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	opts := importer.Options{
		Dir:             dir,
		Provider:        provider,
		ProviderVersion: provVersion,
		SkipInit:        noInit,
		Strict:          strict,
		Verbose:         verbose,
		Types:           types,
		Config:          config,
		WrapperState:    wrapperState,
	}

	// Wire provider-specific post-import steps and import ID builder.
	// Provider detection is in the CLI layer; the importer package itself
	// remains provider-agnostic.
	if strings.Contains(provider, "juju") {
		opts.PostImportSteps = []importer.PostImportStep{
			&importer.JujuNullNormalization{},
			&importer.JujuSchemaVersions{},
			&importer.JujuOfferDefaults{},
			&importer.JujuModelUUIDInjection{},
		}
		opts.BuildImportID = importer.JujuBuildImportID
	}

	if listOnly {
		stop := startSpinner("Preparing provider and reading schema…")
		res, err := importer.Discover(ctx, opts)
		stop()
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Importable list resources (terraform %s):\n", res.TerraformVersion)
		for _, lr := range res.Available {
			fmt.Printf("  %s", lr.Type)
			if names := configAttrNames(lr); names != "" {
				fmt.Printf("  (config: %s)", names)
			}
			fmt.Println()
		}
		return nil
	}

	stop := startSpinner("Matching live resources to module addresses…")
	res, err := importer.Generate(ctx, opts)
	stop()
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Wrote query file:  %s\n", res.QueryFilePath)
	fmt.Fprintf(os.Stderr, "Queried types:     %s\n", strings.Join(typeList(res.Selected), ", "))
	if len(res.Skipped) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped types:     %s\n", strings.Join(res.Skipped, ", "))
		fmt.Fprintln(os.Stderr, "  (these errored during the query — e.g. a facade unsupported on this")
		fmt.Fprintln(os.Stderr, "  model kind. Re-run with --strict to make such errors fatal instead.)")
	}

	if len(res.IDs) == 0 {
		fmt.Fprintln(os.Stderr, "\nNo live resources matched a resource your module wants to create.")
		if len(res.UnmatchedLive) > 0 {
			fmt.Fprintf(os.Stderr, "(%d live resource(s) found but none map to an unmanaged module address.)\n", len(res.UnmatchedLive))
		}
		return nil
	}

	fmt.Fprintf(os.Stderr, "Matched %d resource(s):\n", len(res.IDs))
	for addr := range res.IDs {
		fmt.Fprintf(os.Stderr, "  %s  (import ID: %s)\n", addr, res.IDs[addr])
	}

	if len(res.UnmatchedPlanned) > 0 {
		fmt.Fprintf(os.Stderr, "\nUnmatched module resources (no single live object identified): %d\n", len(res.UnmatchedPlanned))
		for _, p := range res.UnmatchedPlanned {
			fmt.Fprintf(os.Stderr, "  ? %s\n", p.Address)
		}
		fmt.Fprintln(os.Stderr, "  (zero or ambiguous live matches — import these manually if needed.)")
	}
	if len(res.UnmatchedLive) > 0 {
		fmt.Fprintf(os.Stderr, "\nUnmatched live resources (not declared by your module): %d\n", len(res.UnmatchedLive))
		fmt.Fprintln(os.Stderr, "  (e.g. implicit/default resources; left alone.)")
	}

	if len(res.Imported) > 0 {
		fmt.Fprintf(os.Stderr, "\nImported %d resource(s) into state:\n", len(res.Imported))
		for _, r := range res.Imported {
			fmt.Fprintf(os.Stderr, "  \u2713 %s\n", r.Address)
		}
	}

	return nil
}

// setupSourceModule clones a remote module source, writes an Atelier wrapper,
// and returns the directory containing the wrapper (ready for import) plus the
// parsed wrapper state (with variable declarations from the module). If the
// repo has multiple module candidates and --module was not given, it prints the
// candidates and exits with an error.
func setupSourceModule(dir, source, modulePath, ref string) (string, *wrapper.State, error) {
	// Check terraform is available.
	if err := tfexecLocate(); err != nil {
		return "", nil, err
	}

	// If the directory already has a wrapper, re-hydrate its state by
	// re-cloning the module (needed for variable declarations used by
	// post-import normalisation).
	mainPath := filepath.Join(dir, wrapper.MainTF)
	if _, err := os.Stat(mainPath); err == nil {
		fmt.Fprintln(os.Stderr, "Wrapper already exists; loading module variables…")
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
		res, err := bootstrap.LoadExisting(ctx, dir, nil)
		if err != nil {
			return "", nil, err
		}
		return dir, res.State, nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	stop := startSpinner("Cloning and preparing module…")
	res, err := bootstrap.InitNew(ctx, bootstrap.InitOptions{
		WrapperDir: dir,
		Source:     source,
		Ref:        ref,
		ModulePath: modulePath,
	})
	stop()
	if err != nil {
		return "", nil, err
	}

	if res.State == nil {
		// Multiple candidates — user needs --module.
		fmt.Fprintln(os.Stderr, "Multiple module candidates found. Re-run with --module <path>:")
		for _, c := range res.Candidates {
			label := c.Path
			if c.Name != "" {
				label = fmt.Sprintf("%s — %s", c.Path, c.Name)
			}
			fmt.Fprintln(os.Stderr, "  "+label)
		}
		return "", nil, fmt.Errorf("multiple module candidates; specify one with --module")
	}

	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}

	return dir, res.State, nil
}

// tfexecLocate checks that terraform/tofu is on PATH. It is a thin wrapper
// so import.go doesn't need to import the full tfexec package.
func tfexecLocate() error {
	_, err := importer.LocateTerraform()
	return err
}

func typeList(rs []importer.ListResource) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Type
	}
	return out
}

func resolveProviderSource(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" || strings.Contains(arg, "/") {
		return arg
	}
	return arg + "/" + arg
}

func promptProvider() string {
	fmt.Fprint(os.Stderr, "No provider configured here. Provider to scaffold (e.g. juju), or Enter to skip: ")
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return strings.TrimSpace(sc.Text())
	}
	return ""
}

func configAttrNames(lr importer.ListResource) string {
	names := make([]string, 0, len(lr.ConfigAttrs))
	for _, a := range lr.ConfigAttrs {
		n := a.Name
		if a.Required {
			n += "*"
		}
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}

// hclIdentRe matches valid HCL identifiers: [a-zA-Z_][a-zA-Z0-9_-]*.
var hclIdentRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

// validateVarKey checks that a --var key is a valid HCL identifier.
func validateVarKey(k string) error {
	if !hclIdentRe.MatchString(k) {
		return fmt.Errorf("--var key %q is not a valid identifier (must match [a-zA-Z_][a-zA-Z0-9_-]*)", k)
	}
	return nil
}
