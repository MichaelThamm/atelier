package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/MichaelThamm/atelier/internal/modulesource"
	"github.com/MichaelThamm/atelier/internal/wrapper"
)

// LoadedModule is one module block of a wrapper, fully loaded: its state (with
// variable schema and the values from main.tf) plus the ref identity needed to
// display and switch it.
type LoadedModule struct {
	// Name is the HCL block label. Modules are keyed by label, not by source,
	// so one module may appear at two refs as two blocks.
	Name string
	// State is the module's wrapper state with values overlaid from main.tf.
	State *wrapper.State
	// SourceURL, Ref, and ResolvedSHA are the module's git identity.
	SourceURL   string
	Ref         string
	ResolvedSHA string
	// CloneDir is the local checkout the state was read from.
	CloneDir string
}

// LoadSecondaryModules loads every module block in wrapperDir's main.tf except
// the one labelled primaryBlockName, which the caller has already loaded (it is
// the module `atelier` opened on). It is the shared loader behind multi-module
// wrappers: without it, each command that opens a wrapper would re-clone every
// secondary by hand.
//
// Loading is best-effort per block and concurrent: a block whose clone fails is
// reported in the returned warnings and omitted, because one unreachable
// secondary must not stop the wrapper from opening. The result preserves
// main.tf order.
func LoadSecondaryModules(ctx context.Context, wrapperDir, primaryBlockName string, loader *BlockLoader) ([]LoadedModule, []string, error) {
	blocks, err := wrapper.ReadModuleBlocks(wrapperDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read module blocks: %w", err)
	}

	loadCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	type result struct {
		module LoadedModule
		index  int
		err    error
	}
	var (
		results []result
		mu      sync.Mutex
		wg      sync.WaitGroup
	)
	for i, blk := range blocks {
		// Skip the primary (already loaded) and blocks without a source.
		if blk.Source == "" || blk.Name == primaryBlockName {
			continue
		}
		wg.Add(1)
		go func(i int, blk wrapper.ModuleBlockInfo) {
			defer wg.Done()
			state, cloneDir, sha, err := loader.LoadModuleBlock(loadCtx, wrapperDir, blk.Name, blk.Source)
			if err != nil {
				mu.Lock()
				results = append(results, result{index: i, err: err, module: LoadedModule{Name: blk.Name}})
				mu.Unlock()
				return
			}
			remote, ref := modulesource.Decompose(blk.Source)
			mu.Lock()
			results = append(results, result{index: i, module: LoadedModule{
				Name:        blk.Name,
				State:       state,
				SourceURL:   remote,
				Ref:         ref,
				ResolvedSHA: sha,
				CloneDir:    cloneDir,
			}})
			mu.Unlock()
		}(i, blk)
	}
	wg.Wait()

	sort.Slice(results, func(a, b int) bool { return results[a].index < results[b].index })

	var modules []LoadedModule
	var warnings []string
	for _, r := range results {
		if r.err != nil {
			warnings = append(warnings, fmt.Sprintf("skip module %q: %v", r.module.Name, r.err))
			continue
		}
		modules = append(modules, r.module)
	}
	return modules, warnings, nil
}
