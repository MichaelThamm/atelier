package importer

import (
	"context"
	"fmt"
	"os"

	tfjson "github.com/hashicorp/terraform-json"

	"github.com/MichaelThamm/atelier/internal/state"
)

// NullEmptyNormalization reconciles the one class of post-import diff that is
// purely representational: a state value that is null where the configuration
// wants an empty collection, or non-null-empty where the configuration wants
// null. Provider SDKs are inconsistent about which of the two they store for an
// unset map or list, so an import can land on the opposite representation from
// the one the module computes, producing a permanent no-op diff.
//
// It asks Terraform what the configuration actually wants — it plans the module
// as it stands after import and reads the before/after pairs — rather than
// guessing from variable declarations. Guessing was the previous approach and it
// was wrong: defaults were pooled into a single flat attribute→value map across
// every object-typed wrapper variable and then applied to every imported
// resource, so a resource whose module call never passes an argument inherited a
// default from an unrelated variable that happened to share the attribute name.
// That is how `self-signed-certificates` acquired `resources = {}` and
// `storage_directives = {}` when its module call passes neither, giving it a
// permanent diff against the null the configuration computes. Pooling by name
// also meant `var.model`'s `name` default sat in the same namespace as
// `juju_application`'s `name` attribute.
//
// This step is provider-agnostic: null-versus-empty is a provider-SDK
// representational quirk, not a Juju one.
type NullEmptyNormalization struct{}

func (s *NullEmptyNormalization) Name() string { return "Normalize null/empty attributes" }

func (s *NullEmptyNormalization) Run(_ context.Context, pctx PostImportContext) error {
	if pctx.Plan == nil || len(pctx.Imported) == 0 {
		return nil
	}
	plan, err := pctx.Plan()
	if err != nil {
		// A post-import plan failure is not fatal: the import itself succeeded,
		// and the worst outcome is a cosmetic diff the user will see anyway.
		fmt.Fprintf(os.Stderr, "warning: could not plan for null/empty normalization: %v\n", err)
		return nil
	}
	addrs := make(map[string]bool, len(pctx.Imported))
	for _, r := range pctx.Imported {
		addrs[r.Address] = true
	}
	fixes := EmptyNullFixes(plan, addrs)
	if len(fixes) == 0 {
		return nil
	}
	changed, err := state.SetAttributes(pctx.Dir, fixes)
	if err != nil {
		return fmt.Errorf("normalize null/empty attributes: %w", err)
	}
	if changed > 0 {
		fmt.Fprintf(os.Stderr, "\nNormalized %d null/empty attribute(s) across %d resource(s).\n",
			changed, len(fixes))
	}
	return nil
}

// EmptyNullFixes returns, per resource address, the attributes whose state value
// should be rewritten because the only difference between state and
// configuration is null versus an empty collection.
//
// The rule is deliberately narrow, and that narrowness is the safety property:
//
//   - state null, config empty collection -> write the empty collection
//   - state empty collection, config null -> write null
//   - anything else                       -> leave alone
//
// Because nothing outside those two shapes is ever touched, this cannot paper
// over real drift. A charm revision moving 198 -> 199 is a genuine change and
// stays in the plan; only representational noise is removed. For the same reason
// no filtering by planned action is needed — a replace driven by a real
// attribute change keeps that attribute, and therefore keeps the replace.
//
// Attributes whose planned value is unknown ("known after apply") are skipped:
// there is no concrete value to reconcile against.
func EmptyNullFixes(plan *tfjson.Plan, addrs map[string]bool) map[string]map[string]interface{} {
	if plan == nil {
		return nil
	}
	out := map[string]map[string]interface{}{}
	for _, rc := range plan.ResourceChanges {
		if rc == nil || rc.Change == nil || !addrs[rc.Address] {
			continue
		}
		before, ok := rc.Change.Before.(map[string]interface{})
		if !ok {
			continue
		}
		after, ok := rc.Change.After.(map[string]interface{})
		if !ok {
			continue
		}
		unknown, _ := rc.Change.AfterUnknown.(map[string]interface{})

		for k, av := range after {
			if u, ok := unknown[k]; ok && u == true {
				continue
			}
			bv, present := before[k]
			if !present {
				continue
			}
			switch {
			case bv == nil && isEmptyCollection(av):
				setFix(out, rc.Address, k, av)
			case isEmptyCollection(bv) && av == nil:
				setFix(out, rc.Address, k, nil)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func setFix(out map[string]map[string]interface{}, addr, attr string, val interface{}) {
	if out[addr] == nil {
		out[addr] = map[string]interface{}{}
	}
	out[addr][attr] = val
}

// isEmptyCollection reports whether v is an empty JSON object or array. Empty
// strings and zero numbers are deliberately excluded: unlike collections, those
// are ordinary values a provider may legitimately hold.
func isEmptyCollection(v interface{}) bool {
	switch t := v.(type) {
	case map[string]interface{}:
		return len(t) == 0
	case []interface{}:
		return len(t) == 0
	}
	return false
}
