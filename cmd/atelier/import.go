package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/manifest"
	"github.com/MichaelThamm/atelier/internal/tftypes"
	"github.com/MichaelThamm/atelier/internal/tfvars"
	"github.com/MichaelThamm/atelier/internal/tui"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// runImport implements `atelier import [PROVIDER] [flags]`.
//
// Two modes:
//
//  1. With --source: clone a remote module, write an Atelier wrapper, and
//     import live resources into it. The user's workflow is:
//     atelier import juju --source github.com/org/repo --query-var model_uuid=...
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
		presetNames []string
		noInit      bool
		strict      bool
		verbose     bool
		listOnly    bool
		dryRun      bool
		config      = map[string]string{}
		queryConfig = map[string]string{}
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
		case a == "--dry-run":
			dryRun = true
		case a == "--source" || a == "--module" || a == "--ref" || a == "--type" || a == "--var" || a == "--query-var" || a == "--dir" || a == "--provider-version" || a == "--preset":
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
			case "--preset":
				presetNames = append(presetNames, val)
			case "--var":
				k, v, ok := strings.Cut(val, "=")
				if !ok || k == "" {
					return fmt.Errorf("--var expects KEY=VALUE, got %q", val)
				}
				if err := validateVarKey(k); err != nil {
					return err
				}
				config[k] = v
			case "--query-var":
				k, v, ok := strings.Cut(val, "=")
				if !ok || k == "" {
					return fmt.Errorf("--query-var expects KEY=VALUE, got %q", val)
				}
				if err := validateVarKey(k); err != nil {
					return err
				}
				queryConfig[k] = v
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
		case strings.HasPrefix(a, "--preset="):
			presetNames = append(presetNames, strings.TrimPrefix(a, "--preset="))
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
		case strings.HasPrefix(a, "--query-var="):
			kv := strings.TrimPrefix(a, "--query-var=")
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return fmt.Errorf("--query-var expects KEY=VALUE, got %q", kv)
			}
			if err := validateVarKey(k); err != nil {
				return err
			}
			queryConfig[k] = v
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

	// Apply presets if specified via --preset flags. Presets are loaded from
	// atelier.local.yaml files discovered by walking up from the wrapper
	// directory. Multiple presets are merged in order (later overrides earlier),
	// and --var flags override all preset values.
	if len(presetNames) > 0 && wrapperState != nil {
		if err := applyPresets(dir, wrapperState, presetNames); err != nil {
			return err
		}
	}

	// Merge --var flag values into the wrapper state and persist to main.tf.
	// This ensures both preset values and --var values are visible to
	// terraform plan via main.tf (not just the temp .auto.tfvars, which can't
	// represent complex types correctly).
	if wrapperState != nil {
		// Seed unset module variables from like-named query variables, before
		// anything runs. `terraform query` loads the root module, so it is the
		// first command to reject a module argument the wrapper omits — and a
		// value the user already supplied for the query engine should not have
		// to be supplied a second time as a module input.
		//
		// This cannot be left to the preflight step: preflight runs after the
		// query, so it is too late to stop the query failing. Nor does it need
		// to be, since the value is already in hand.
		if seeded := seedFromQueryVars(wrapperState, queryConfig); len(seeded) > 0 {
			fmt.Fprintf(os.Stderr, "Using query variable(s) for module input(s): %s\n",
				strings.Join(seeded, ", "))
		}
		applyVarOverrides(wrapperState, config)
		// Propagate preset values back into config so that downstream
		// consumers (e.g. JujuBuildImportID which looks up model_uuid
		// in opts.Config) can see values supplied via --preset, not just
		// --var flags.
		mergeWrapperStateIntoConfig(wrapperState, config)
		if err := wrapperState.Write(); err != nil {
			return fmt.Errorf("write values to main.tf: %w", err)
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
		DryRun:          dryRun,
		Types:           types,
		Config:          config,
		QueryConfig:     queryConfig,
		WrapperState:    wrapperState,
	}

	// Wire provider-specific steps and the import ID builder. Provider detection
	// lives in the CLI layer; the importer package itself stays
	// provider-agnostic.
	//
	// Detection considers the directory's own declarations as well as the
	// PROVIDER argument: that argument is only given when Atelier has to
	// scaffold provider config, so a directory that already declares its
	// providers would otherwise wire nothing and import nothing.
	detected := append([]string{provider}, importer.DeclaredProviderSources(dir)...)
	if hasJujuProvider(detected) {
		opts.PreflightSteps = []importer.PreflightStep{
			&importer.JujuModelIdentity{},
		}
		opts.PlanChecks = []importer.PlanCheck{
			&importer.JujuModelConsistency{},
		}
		opts.PostImportSteps = []importer.PostImportStep{
			// Generic: null-versus-empty is a provider-SDK quirk, not a Juju one.
			&importer.NullEmptyNormalization{},
			&importer.JujuSchemaVersions{},
			&importer.JujuOfferDefaults{},
			&importer.JujuModelUUIDInjection{},
		}
		opts.BuildImportID = importer.JujuBuildImportID
	} else {
		// Be explicit rather than letting the run reach the end and report every
		// resource as "could not build an import ID" with no reason given.
		// Import IDs are provider-specific and not derivable from the schema
		// (ADR-0028), so support is an explicit per-provider allow-list.
		fmt.Fprintf(os.Stderr,
			"note: `atelier import` currently implements import IDs for the Juju provider only.\n"+
				"  Detected: %s\n"+
				"  Discovery and matching still run, and every match is reported with the\n"+
				"  address it belongs to — but nothing will be imported, because Atelier\n"+
				"  cannot construct import IDs for this provider. You can use the reported\n"+
				"  matches to run `terraform import` yourself.\n\n",
			describeProviders(detected))
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

	fmt.Fprintf(os.Stderr, "Queried types:     %s\n", strings.Join(typeList(res.Selected), ", "))
	if len(res.Skipped) > 0 {
		fmt.Fprintf(os.Stderr, "Skipped types:     %s\n", strings.Join(res.Skipped, ", "))
		fmt.Fprintln(os.Stderr, "  (these errored during the query — e.g. a facade unsupported on this")
		fmt.Fprintln(os.Stderr, "  model kind. Re-run with --strict to make such errors fatal instead.)")
	}
	if res.QueryFileRetainedReason != "" {
		fmt.Fprintf(os.Stderr, "Kept query file:   %s\n", res.QueryFilePath)
		fmt.Fprintf(os.Stderr, "  (%s — re-run it by hand to reproduce.)\n", res.QueryFileRetainedReason)
	}

	// One linear report. Earlier this had an early return for the zero-import
	// case, which skipped the unmatched-planned list and the dry-run preview —
	// precisely the runs where both matter most.
	switch {
	case len(res.IDs) > 0:
		fmt.Fprintf(os.Stderr, "Matched %d resource(s):\n", len(res.IDs))
		for _, addr := range sortedKeys(res.IDs) {
			fmt.Fprintf(os.Stderr, "  %s  (import ID: %s)\n", addr, res.IDs[addr])
		}
	case res.MatchedCount > 0:
		// Distinguish "already done" from "could not match anything" — these
		// look identical in the shape of the run but mean opposite things.
		fmt.Fprintf(os.Stderr, "\nNothing to import: all %d matched resource(s) are already in state.\n", res.MatchedCount)
	default:
		fmt.Fprintln(os.Stderr, "\nNo live resources matched a resource your module wants to create.")
	}

	reportUnmatchedPlanned(res)
	if len(res.UnmatchedLive) > 0 {
		reportUnmatchedLive(res)
	}
	reportUnresolvedIDs(res)

	if res.Preview != nil {
		reportDryRun(res)
		return nil
	}

	if len(res.Imported) > 0 {
		fmt.Fprintf(os.Stderr, "\nImported %d resource(s) into state:\n", len(res.Imported))
		for _, r := range res.Imported {
			fmt.Fprintf(os.Stderr, "  \u2713 %s\n", r.Address)
		}
	}

	return nil
}

// reportDryRun summarises a dry run. The number that matters is Add: those are
// resources the module declares that the import set does *not* cover, so they
// would be created — duplicating live infrastructure — if the artifact were
// applied as-is. Terraform-internal types that can never be imported are
// counted separately because they are expected.
func reportDryRun(res *importer.Result) {
	p := res.Preview
	fmt.Fprintf(os.Stderr, "\nDry run — nothing was imported. Terraform state is untouched.\n")
	if res.ImportsFilePath != "" {
		fmt.Fprintf(os.Stderr, "Wrote import artifact: %s\n", res.ImportsFilePath)
	} else {
		fmt.Fprintln(os.Stderr, "No import artifact written (nothing to import).")
	}
	fmt.Fprintf(os.Stderr, "\nPreview: %d to import, %d to add, %d to change, %d to destroy.\n",
		p.Import, p.Add, p.Change, p.Destroy)
	if p.UnimportableAdds > 0 {
		fmt.Fprintf(os.Stderr, "  %d of those %d additions are Terraform-internal types with no live\n", p.UnimportableAdds, p.Add)
		fmt.Fprintln(os.Stderr, "  counterpart (e.g. terraform_data), and are expected.")
	}
	if len(p.AddAddresses) > 0 {
		fmt.Fprintf(os.Stderr, "\n  %d resource(s) would be CREATED, not imported:\n", len(p.AddAddresses))
		for _, a := range p.AddAddresses {
			fmt.Fprintf(os.Stderr, "    + %s\n", a)
		}
		fmt.Fprintln(os.Stderr, "  If these already exist, applying the artifact as-is would duplicate")
		fmt.Fprintln(os.Stderr, "  them. Resolve them before importing.")
	} else {
		fmt.Fprintln(os.Stderr, "\n  Every importable resource the module declares is covered.")
	}
	fmt.Fprintf(os.Stderr, "\nTo perform the import, re-run without --dry-run (uses `terraform import`,\n")
	fmt.Fprintln(os.Stderr, "which only writes state and cannot change infrastructure).")
	if res.ImportsFilePath != "" {
		fmt.Fprintf(os.Stderr, "%s is cleared automatically before the next run's plan.\n",
			importer.DefaultImportsFile)
	}
}

// maxLiveNamesShown caps the per-type sample so a deployment with dozens of
// auto-created secrets does not bury the rest of the report.
const maxLiveNamesShown = 3

// maxLiveNameLen bounds each name so a line stays readable. Juju identities
// carry a 36-character model UUID prefix and certificate secrets carry a
// 64-character hash, either of which would wrap the terminal.
const maxLiveNameLen = 44

// elide shortens s to at most maxLiveNameLen characters, keeping both ends. The
// distinguishing part of a generated identifier can be at either end — a
// prefixed model UUID puts it at the tail, a hashed suffix puts it at the head —
// so trimming the middle is the only choice that does not depend on knowing the
// provider's ID format.
func elide(s string) string {
	if len(s) <= maxLiveNameLen {
		return s
	}
	const head, tail = 10, 30
	return s[:head] + "…" + s[len(s)-tail:]
}

// reportUnmatchedLive lists live objects that map to no module address, grouped
// by resource type. These are left alone, and most are genuinely implicit —
// peer relations, auto-created secrets, default storage pools. But an object
// here that you expected the module to manage means the module is not declaring
// it, usually because a count or for_each is disabled by a variable, so the
// address it would occupy does not exist in the plan. Showing them makes that
// diagnosable instead of invisible.
func reportUnmatchedLive(res *importer.Result) {
	fmt.Fprintf(os.Stderr, "\nUnmatched live resources (not declared by your module): %d\n", len(res.UnmatchedLive))
	for _, g := range importer.GroupUnmatchedLive(res.UnmatchedLive) {
		shown := g.Names
		suffix := ""
		if len(shown) > maxLiveNamesShown {
			shown = shown[:maxLiveNamesShown]
			suffix = fmt.Sprintf(", … (+%d more)", g.Count-maxLiveNamesShown)
		}
		elided := make([]string, len(shown))
		for i, n := range shown {
			elided[i] = elide(n)
		}
		fmt.Fprintf(os.Stderr, "  %-18s %3d  %s%s\n", g.Type, g.Count, strings.Join(elided, ", "), suffix)
	}
	fmt.Fprintln(os.Stderr, "  Left alone. If your module should be managing one of these, it is not")
	fmt.Fprintln(os.Stderr, "  declaring it — check for a count/for_each disabled by a variable.")
	fmt.Fprintln(os.Stderr, "  (--verbose lists every live object in full.)")
}

// sortedKeys returns a map's keys in sorted order, so reports are stable across
// runs rather than following Go's randomised map iteration.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// reportUnmatchedPlanned lists module resources for which no single live object
// could be identified. Reported on every run, including those that imported
// nothing: a run where nothing resolved is exactly when this list is the whole
// story.
func reportUnmatchedPlanned(res *importer.Result) {
	if len(res.UnmatchedPlanned) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\nUnmatched module resources (no single live object identified): %d\n", len(res.UnmatchedPlanned))
	for _, p := range res.UnmatchedPlanned {
		fmt.Fprintf(os.Stderr, "  ? %s (%s)\n", p.Address, p.Type)
	}
	fmt.Fprintln(os.Stderr, "  (zero or ambiguous live matches — import these manually if needed.)")
}

// reportUnresolvedIDs surfaces resources that were matched to a live object but
// could not be imported because no import ID could be constructed. Without this
// they appear in neither the matched nor the unmatched list, so the module ends
// up silently missing resources that a later apply would try to create.
func reportUnresolvedIDs(res *importer.Result) {
	if len(res.UnresolvedIDs) == 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "\nMatched but NOT imported (could not build an import ID): %d\n", len(res.UnresolvedIDs))
	for _, m := range res.UnresolvedIDs {
		fmt.Fprintf(os.Stderr, "  ! %s (%s)\n", m.Address, m.ResourceType)
	}
	fmt.Fprintln(os.Stderr, "  A later apply would try to CREATE these, duplicating live resources —")
	fmt.Fprintln(os.Stderr, "  import them manually first. Each was matched to a live object, so the")
	fmt.Fprintln(os.Stderr, "  address is right; only the provider-specific ID could not be built.")
}

// hasJujuProvider reports whether any of the given provider source addresses is
// the Juju provider.
func hasJujuProvider(sources []string) bool {
	for _, s := range sources {
		if strings.Contains(s, "juju") {
			return true
		}
	}
	return false
}

// describeProviders renders the detected provider sources for a diagnostic,
// falling back to a clear phrase when none could be determined.
func describeProviders(sources []string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sources {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return "no provider could be determined from the arguments or the directory"
	}
	return strings.Join(out, ", ")
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

// seedFromQueryVars fills module variables the wrapper leaves unset with
// like-named --query-var values, returning the names it set.
//
// The two flag families are deliberately separate: --query-var configures the
// query engine's list blocks and is not in general a module input. But when the
// module happens to declare a variable of the same name, the user has already
// stated the value, and requiring it twice is friction with no purpose. The Juju
// provider makes this the common case: model_uuid is a required config attribute
// on most of its list resources, so every real import supplies it — and a module
// such as loki-operators also declares model_uuid as a required input.
//
// Only unset variables are seeded. A value the user chose is never overwritten,
// even when it disagrees; a genuine disagreement is caught later by a plan check
// and refused, which is better than silently rewriting their configuration.
func seedFromQueryVars(state *wrapper.State, queryConfig map[string]string) []string {
	if state == nil || len(queryConfig) == 0 {
		return nil
	}
	var seeded []string
	for _, name := range sortedKeys(queryConfig) {
		v := state.FindVar(name)
		if v == nil {
			continue // not a module input; nothing to do
		}
		if cur, ok := state.VariableValue(name); ok && cur != cty.NilVal && !cur.IsNull() {
			continue // already set — leave the user's value alone
		}
		val := convertStringToCty(queryConfig[name], v)
		if val == cty.NilVal {
			continue
		}
		state.EnsureValues()
		state.Values[name] = val
		seeded = append(seeded, name)
	}
	return seeded
}

// applyVarOverrides merges --var flag values into the wrapper state, converting
// string values to typed cty.Values based on the variable declarations.
func applyVarOverrides(state *wrapper.State, config map[string]string) {
	for varName, strVal := range config {
		v := state.FindVar(varName)
		if v == nil {
			continue
		}
		val := convertStringToCty(strVal, v)
		if val != cty.NilVal {
			state.Values[varName] = val
		}
	}
}

// mergeWrapperStateIntoConfig propagates string-typed values from the wrapper
// state back into the flat config map. This ensures that values supplied via
// --preset (which only update wrapperState.Values) are visible to downstream
// consumers that look up keys in opts.Config — e.g. JujuBuildImportID needs
// model_uuid to construct import IDs for juju_application and juju_secret.
// Keys already present in config (--var flags) take precedence.
func mergeWrapperStateIntoConfig(state *wrapper.State, config map[string]string) {
	if state == nil {
		return
	}
	for k, v := range state.Values {
		if v.IsNull() || v == cty.NilVal {
			continue
		}
		if v.Type() != cty.String {
			continue
		}
		if _, exists := config[k]; exists {
			continue // --var flag already set; don't override
		}
		config[k] = v.AsString()
	}
}

// applyPresets loads the named presets from atelier.local.yaml files and
// applies them to the wrapper state. Presets are merged in order (later
// overrides earlier).
func applyPresets(dir string, state *wrapper.State, presetNames []string) error {
	// Load all available presets from atelier.local.yaml files.
	rawPresets, warns := manifest.LoadLocalPresets(dir, modulePathFromState(state))
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "warning:", w)
	}
	if len(rawPresets) == 0 {
		return fmt.Errorf("no presets found in atelier.local.yaml files")
	}

	// Resolve presets to typed cty.Values.
	resolvedPresets := tui.ResolvePresets(rawPresets, state.Vars)
	if len(resolvedPresets) == 0 {
		return fmt.Errorf("no presets resolved for module")
	}

	// Build a lookup map for quick access by name.
	presetByName := make(map[string]tui.ResolvedPreset, len(resolvedPresets))
	for _, p := range resolvedPresets {
		presetByName[p.Name] = p
	}

	// Apply presets in order (later overrides earlier).
	appliedPresets := make(map[string]bool)
	for _, name := range presetNames {
		p, ok := presetByName[name]
		if !ok {
			// List available presets for a helpful error message.
			available := make([]string, 0, len(resolvedPresets))
			for _, rp := range resolvedPresets {
				available = append(available, rp.Name)
			}
			return fmt.Errorf("preset %q not found; available presets: %v", name, available)
		}
		appliedPresets[name] = true

		// Merge preset values into wrapper state.
		for varName, val := range p.Values {
			state.Values[varName] = val
		}
	}

	// Print which presets were applied.
	if len(appliedPresets) > 0 {
		names := make([]string, 0, len(appliedPresets))
		for name := range appliedPresets {
			names = append(names, name)
		}
		fmt.Fprintf(os.Stderr, "Applied preset(s): %s\n", strings.Join(names, ", "))
	}

	return nil
}

// convertStringToCty converts a string value to a cty.Value based on the
// variable's declared type. This is used to convert --var flag values to
// typed values for the wrapper state.
func convertStringToCty(strVal string, v *tfvars.Variable) cty.Value {
	if v == nil || v.Type == nil {
		// No type info; treat as string.
		return cty.StringVal(strVal)
	}

	typ := v.Type
	switch typ.Kind {
	case tftypes.KindString:
		return cty.StringVal(strVal)
	case tftypes.KindBool:
		switch strings.ToLower(strVal) {
		case "true", "1", "yes":
			return cty.True
		case "false", "0", "no":
			return cty.False
		default:
			return cty.NilVal // invalid bool
		}
	case tftypes.KindNumber:
		// Try to parse as a number.
		// First, try to parse as an integer.
		var n int64
		if _, err := fmt.Sscanf(strVal, "%d", &n); err == nil {
			return cty.NumberIntVal(n)
		}
		// Then, try to parse as a float.
		var f float64
		if _, err := fmt.Sscanf(strVal, "%f", &f); err == nil {
			return cty.NumberFloatVal(f)
		}
		return cty.NilVal // invalid number
	case tftypes.KindObject, tftypes.KindMap, tftypes.KindList, tftypes.KindSet:
		// Parse HCL expressions (objects, maps, lists, sets).
		expr, diags := hclsyntax.ParseExpression([]byte(strVal), "", hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return cty.NilVal
		}
		val, diags := expr.Value(nil)
		if diags.HasErrors() {
			return cty.NilVal
		}
		return val
	default:
		// For any other types, return nil and let terraform handle it.
		return cty.NilVal
	}
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
