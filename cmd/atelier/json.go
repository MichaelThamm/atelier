package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/MichaelThamm/atelier/internal/bootstrap"
	"github.com/MichaelThamm/atelier/internal/importer"
	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// This file is the machine-readable output for the commands whose result is data
// rather than a diff. Every payload is wrapped in one envelope, so a consumer
// can tell what it got and which revision of the shape it is reading:
//
//	{"schema":1,"command":"ls","data":{...}}
//
// Text output is unchanged. `--json` adds a payload on stdout, which is already
// the result channel, and leaves the human report where it is: on stderr. That
// way `--json` cannot suppress the explanation a user needs when a run goes
// wrong, and the two renderings cannot disagree because both are built from the
// same values. See ADR-0048.

// jsonSchemaVersion is the revision of the payload shapes in this file. It
// changes when a field is removed or its meaning changes; adding a field is not
// a change, so consumers can ignore what they do not recognise.
const jsonSchemaVersion = 1

// jsonEnvelope wraps every --json payload.
type jsonEnvelope struct {
	Schema  int    `json:"schema"`
	Command string `json:"command"`
	Data    any    `json:"data"`
}

// renderJSON writes one --json payload: indented so a CI log is readable
// without jq, and newline-terminated so it composes in a shell.
func renderJSON(w io.Writer, command string, data any) error {
	body, err := json.MarshalIndent(
		jsonEnvelope{Schema: jsonSchemaVersion, Command: command, Data: data}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s report: %w", command, err)
	}
	_, err = fmt.Fprintf(w, "%s\n", body)
	return err
}

// --- shared shapes -----------------------------------------------------------

// jsonModule is one module block, decomposed into exactly the three values
// `atelier add` takes. The text table prints the repository and the ref and
// drops the //subdir; a payload that lost information would make a caller
// re-parse the address, which is the thing this output exists to avoid.
type jsonModule struct {
	Name string `json:"name"`
	// Source is the repository URL or local path, with neither the //subdir nor
	// the ?ref= query.
	Source string `json:"source"`
	// ModulePath is the //subdir, or null when the block points at the
	// repository root.
	ModulePath *string `json:"modulePath"`
	// Ref is the pinned ref, or null when the block pins none. Null rather than
	// absent throughout, so a consumer can tell "no value" from a field it does
	// not know about.
	Ref *string `json:"ref"`
}

func newJSONModule(name, address string) jsonModule {
	_, ref := modulesource.Decompose(address)
	m := jsonModule{Name: name, Source: modulesource.Remote(address)}
	if path := modulesource.ModulePath(address); path != "" {
		m.ModulePath = &path
	}
	if ref != "" {
		m.Ref = &ref
	}
	return m
}

// jsonResource is a resource a report could not account for.
type jsonResource struct {
	Address string `json:"address"`
	Type    string `json:"type"`
}

// --- atelier ls --------------------------------------------------------------

// jsonLs is the `atelier ls` payload.
type jsonLs struct {
	// IsWrapper is false when the directory holds no main.tf. The text report
	// distinguishes that from a wrapper declaring no modules, and so does this.
	IsWrapper bool         `json:"isWrapper"`
	Modules   []jsonModule `json:"modules"`
}

// lsPayload builds the `atelier ls` payload. The ref is decomposed off the
// source address exactly as the text table does, so the two agree.
func lsPayload(isWrapper bool, blocks []wrapper.ModuleBlockInfo) jsonLs {
	modules := make([]jsonModule, 0, len(blocks))
	for _, b := range blocks {
		modules = append(modules, newJSONModule(b.Name, b.Source))
	}
	return jsonLs{IsWrapper: isWrapper, Modules: modules}
}

// --- atelier add -------------------------------------------------------------

// jsonAdd is the `atelier add` payload.
type jsonAdd struct {
	// Wrapper is the directory the block was written into. `atelier add` creates
	// a directory named after the module when it is not told where to write, so
	// this is the answer to "where did my wrapper go?".
	Wrapper string     `json:"wrapper"`
	Added   jsonModule `json:"added"`
	// Blocks is every module in the wrapper afterwards.
	Blocks []string `json:"blocks"`
}

// addPayload builds the `atelier add` payload. Blocks lists every module in the
// wrapper afterwards, so a composing caller sees the result without a second
// `atelier ls`.
func addPayload(out addOutcome) jsonAdd {
	names := make([]string, 0, len(out.All))
	for _, b := range out.All {
		names = append(names, b.Name)
	}
	return jsonAdd{Wrapper: out.Dir, Added: out.Block, Blocks: names}
}

// jsonVarFiles is the `atelier add --list-var-files` payload.
type jsonVarFiles struct {
	Bundles []jsonVarFile `json:"bundles"`
}

// jsonVarFile is one `.tfvars` bundle, as the listing and the TUI picker see it.
type jsonVarFile struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	Display     string `json:"display"`
	Description string `json:"description"`
}

func varFilesPayload(files []bootstrap.VarFile) jsonVarFiles {
	bundles := make([]jsonVarFile, 0, len(files))
	for _, f := range files {
		bundles = append(bundles, jsonVarFile{
			Name:        f.Name,
			Path:        f.Path,
			Source:      f.Source,
			Display:     f.Display,
			Description: f.Description,
		})
	}
	return jsonVarFiles{Bundles: bundles}
}

// --- atelier wrappers --------------------------------------------------------

// jsonWrappers is the `atelier wrappers` payload.
type jsonWrappers struct {
	Wrappers []jsonWrapper `json:"wrappers"`
}

// jsonWrapper is one discovered wrapper directory.
type jsonWrapper struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Modules are block names, sorted. Empty when the wrapper declares none,
	// which the text table prints as `-`.
	Modules []string `json:"modules"`
}

func wrappersPayload(found []childWrapper, parent string) jsonWrappers {
	wrappers := make([]jsonWrapper, 0, len(found))
	for _, w := range found {
		modules := w.Modules
		if modules == nil {
			modules = []string{}
		}
		wrappers = append(wrappers, jsonWrapper{
			Name:    w.Name,
			Path:    filepath.Join(parent, w.Name),
			Modules: modules,
		})
	}
	return jsonWrappers{Wrappers: wrappers}
}

// --- atelier import ----------------------------------------------------------

// jsonImport is the `atelier import` payload.
type jsonImport struct {
	// Matched maps each matched module address to the import ID used for it.
	// Non-empty means something was found; whether it still needed importing is
	// what AlreadyInState distinguishes.
	Matched map[string]string `json:"matched"`
	// Imported lists the addresses written into state. Empty for a dry run.
	Imported []string `json:"imported"`
	// AlreadyInState and MatchedNothing are the two benign-looking empty runs
	// the text report distinguishes: everything matched is already there, versus
	// nothing matched at all. They mean opposite things — a re-run, or a wrong
	// model — and neither is derivable from the counts above.
	AlreadyInState bool `json:"alreadyInState"`
	MatchedNothing bool `json:"matchedNothing"`
	// Unresolved lists resources that matched a live object but whose import ID
	// could not be built. A later apply would create them, duplicating live
	// infrastructure.
	Unresolved []jsonResource `json:"unresolved"`
	// UnmatchedModule lists module resources with no live match; an apply would
	// create them too.
	UnmatchedModule []jsonResource `json:"unmatchedModule"`
	// UnmatchedLive lists live objects no module resource claimed. Unlike the
	// text report, names are neither elided nor truncated: a machine consumer
	// should get everything and decide how to show it.
	UnmatchedLive []jsonLiveGroup `json:"unmatchedLive"`
	QueriedTypes  []string        `json:"queriedTypes"`
	SkippedTypes  []string        `json:"skippedTypes"`
	DryRun        bool            `json:"dryRun"`
	Preview       *jsonPreview    `json:"preview"`
	// PostImportPlan is the plan against the state this run's imports produced,
	// so a non-empty add or change means a later apply would still alter the
	// deployment. Null unless the run computed it.
	PostImportPlan *jsonPostImportPlan `json:"postImportPlan"`
	// ImportsFile and QueryFile are absolute paths to artifacts this run wrote,
	// or "" when it wrote none. QueryFile is retained only when it would help a
	// retry.
	ImportsFile string `json:"importsFile"`
	QueryFile   string `json:"queryFile"`
	// TerraformVersion is the resolved Terraform the query ran against.
	TerraformVersion string `json:"terraformVersion"`
}

// jsonLiveGroup is one resource type's worth of unmatched live objects.
type jsonLiveGroup struct {
	Type  string   `json:"type"`
	Count int      `json:"count"`
	Names []string `json:"names"`
}

// jsonPreview is the plan summary a dry run produces.
type jsonPreview struct {
	ToImport         int      `json:"toImport"`
	Add              int      `json:"add"`
	Change           int      `json:"change"`
	Destroy          int      `json:"destroy"`
	UnimportableAdds int      `json:"unimportableAdds"`
	AddAddresses     []string `json:"addAddresses"`
}

// jsonPostImportPlan is the plan against the imported state. It carries no
// "toImport": there is nothing left to import by the time it is taken, and
// reporting a zero there would read as a shortfall rather than as done.
type jsonPostImportPlan struct {
	Add              int      `json:"add"`
	Change           int      `json:"change"`
	Destroy          int      `json:"destroy"`
	UnimportableAdds int      `json:"unimportableAdds"`
	AddAddresses     []string `json:"addAddresses"`
	ChangeAddresses  []string `json:"changeAddresses"`
}

// importPayload builds the `atelier import` payload from the same Result the
// text report reads, so the two cannot drift.
func importPayload(res *importer.Result, dryRun bool) jsonImport {
	out := jsonImport{
		Matched:          make(map[string]string, len(res.IDs)),
		Imported:         make([]string, 0, len(res.Imported)),
		Unresolved:       make([]jsonResource, 0, len(res.UnresolvedIDs)),
		UnmatchedModule:  make([]jsonResource, 0, len(res.UnmatchedPlanned)),
		UnmatchedLive:    make([]jsonLiveGroup, 0),
		QueriedTypes:     typeList(res.Selected),
		SkippedTypes:     nonNil(res.Skipped),
		DryRun:           dryRun,
		ImportsFile:      res.ImportsFilePath,
		QueryFile:        res.QueryFilePath,
		TerraformVersion: res.TerraformVersion,
	}
	for addr, id := range res.IDs {
		out.Matched[addr] = id
	}
	for _, r := range res.Imported {
		out.Imported = append(out.Imported, r.Address)
	}
	for _, m := range res.UnresolvedIDs {
		out.Unresolved = append(out.Unresolved, jsonResource{Address: m.Address, Type: m.ResourceType})
	}
	for _, p := range res.UnmatchedPlanned {
		out.UnmatchedModule = append(out.UnmatchedModule, jsonResource{Address: p.Address, Type: p.Type})
	}
	for _, g := range importer.GroupUnmatchedLive(res.UnmatchedLive) {
		out.UnmatchedLive = append(out.UnmatchedLive, jsonLiveGroup{
			Type: g.Type, Count: g.Count, Names: nonNil(g.Names),
		})
	}
	// Mirrors the three-way switch the text report makes: some matched, all of
	// them already in state, or nothing matched at all.
	switch {
	case len(res.IDs) > 0:
	case res.MatchedCount > 0:
		out.AlreadyInState = true
	default:
		out.MatchedNothing = true
	}
	if p := res.Preview; p != nil {
		out.Preview = &jsonPreview{
			ToImport:         p.Import,
			Add:              p.Add,
			Change:           p.Change,
			Destroy:          p.Destroy,
			UnimportableAdds: p.UnimportableAdds,
			AddAddresses:     nonNil(p.AddAddresses),
		}
	}
	if p := res.PostImportPlan; p != nil {
		out.PostImportPlan = &jsonPostImportPlan{
			Add:              p.Add,
			Change:           p.Change,
			Destroy:          p.Destroy,
			UnimportableAdds: p.UnimportableAdds,
			AddAddresses:     nonNil(p.AddAddresses),
			ChangeAddresses:  nonNil(p.ChangeAddresses),
		}
	}
	return out
}

// jsonListResources is the `atelier import --list` payload.
type jsonListResources struct {
	TerraformVersion string             `json:"terraformVersion"`
	Available        []jsonListResource `json:"available"`
}

// jsonListResource is one queryable list resource.
type jsonListResource struct {
	Type          string           `json:"type"`
	ProviderKey   string           `json:"providerKey"`
	ProviderLocal string           `json:"providerLocal"`
	ConfigAttrs   []jsonConfigAttr `json:"configAttrs"`
}

// jsonConfigAttr is one config argument a list block accepts.
type jsonConfigAttr struct {
	Name string `json:"name"`
	// Required marks an argument the query cannot run without, which the text
	// report marks with a trailing `*`.
	Required bool `json:"required"`
}

func listResourcesPayload(res *importer.Result) jsonListResources {
	available := make([]jsonListResource, 0, len(res.Available))
	for _, lr := range res.Available {
		attrs := make([]jsonConfigAttr, 0, len(lr.ConfigAttrs))
		for _, a := range lr.ConfigAttrs {
			attrs = append(attrs, jsonConfigAttr{Name: a.Name, Required: a.Required})
		}
		available = append(available, jsonListResource{
			Type:          lr.Type,
			ProviderKey:   lr.ProviderKey,
			ProviderLocal: lr.ProviderLocal,
			ConfigAttrs:   attrs,
		})
	}
	return jsonListResources{TerraformVersion: res.TerraformVersion, Available: available}
}

// nonNil returns an empty slice for nil, so a JSON array is `[]` and never
// `null`. A consumer iterating a field should not have to distinguish the two.
func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
