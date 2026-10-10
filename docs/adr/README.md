# Architecture Decision Records

Each ADR records a single architectural decision: its context, what was
decided, what alternatives were considered, and what the consequences are.
Format follows the Michael Nygard template, lightly extended.

## Index

| #    | Title                                                                                       | Status   |
|------|---------------------------------------------------------------------------------------------|----------|
| 0001 | [Wrapper module as durable artifact](0001-wrapper-as-durable-artifact.md)                   | Accepted |
| 0002 | [Author + plan + apply scope](0002-author-and-plan-scope.md)                                | Accepted |
| 0003 | [GitOps loading model](0003-gitops-loading.md)                                              | Accepted |
| 0004 | [Wrapper layout](0004-wrapper-layout.md)                                                    | Accepted |
| 0005 | [Implementation language: Go](0005-implementation-language-go.md)                           | Accepted |
| 0006 | [Two-pane TUI layout](0006-two-pane-ui-layout.md)                                           | Accepted |
| 0007 | [Sparse-plus-required wrapper-write rule](0007-sparse-wrapper-write-rule.md)                | Accepted |
| 0008 | [Provider schema via `terraform providers schema -json`](0008-provider-schema-discovery.md) | Accepted |
| 0009 | [Secrets handling](0009-secrets-handling.md)                                                | Superseded |
| 0010 | [Manifest format: `atelier.yaml`](0010-manifest-format.md)                                  | Superseded by ADR-0022 |
| 0011 | [Plan output as module-path tree](0011-plan-output-tree.md)                                 | Accepted |
| 0012 | [Validation via `terraform validate`](0012-validation-via-terraform-validate.md)            | Accepted |
| 0014 | [Unified layout budget and scroll support](0014-unified-layout-budget.md)                   | Accepted |
| 0015 | [Multi-module grouping in the left pane](0015-multi-module-grouping.md)                     | Accepted |
| 0016 | [Scope boundaries — no orchestration overlap with Terragrunt](0016-scope-boundaries-no-orchestration.md) | Accepted |
| 0017 | [Inter-module wiring in the TUI](0017-inter-module-wiring.md)                               | Accepted |
| 0018 | [Additive `atelier module` command](0018-additive-module-command.md)                       | Superseded by ADR-0038 |
| 0019 | [Unified module version display](0019-unified-module-version-display.md)                    | Proposed |
| 0020 | [Readline-style text editing in variable editors](0020-readline-style-text-editing.md)      | Proposed |

| 0022 | [Local presets: `atelier.local.yaml`](0022-local-presets.md)                                | Superseded by ADR-0031 |
| 0023 | [Map / map(object) row editing lifecycle](0023-map-row-editing-lifecycle.md)                | Proposed |
| 0024 | [Surfacing Terraform `check` block warnings](0024-check-block-warnings.md)                   | Accepted |
| 0025 | [Interactive ref selection in the ref-switch modal](0025-ref-selection-matcher.md)          | Proposed |
| 0026 | [Generate a preset from the current configuration](0026-save-preset.md)                      | Superseded by ADR-0031 |
| 0027 | [`atelier import` — import live infrastructure into an existing module](0027-atelier-import.md) | Accepted |
| 0028 | [Provider-specific import IDs — scoped to Juju for v1](0028-provider-specific-import-ids.md) | Accepted |
| 0029 | [Live logs view (`L`)](0029-live-logs-view.md)                                              | Superseded by ADR-0052 |
| 0030 | [Target-directory preflight and duplicate-module refusal](0030-target-directory-preflight.md) | Accepted |
| 0031 | [Presets as Terraform-native `.tfvars` bundles](0031-presets-as-tfvars-bundles.md)          | Superseded by ADR-0053 |
| 0032 | [Nested-wrapper preflight distinguishes deliberate nesting from stray `cd`](0032-nested-wrapper-preflight-scope.md) | Accepted |
| 0033 | [Provider-specific matching lives behind the Provider interface](0033-provider-fallback-matching.md) | Accepted |
| 0034 | [`atelier module apply` one-liner](0034-module-apply-one-liner.md)                            | Accepted |
| 0035 | [Bundled module gallery with a scaffold-and-validate CI gate](0035-bundled-module-gallery.md) | Superseded by ADR-0039 |
| 0036 | [Read-only wrapper discovery (`atelier wrappers`)](0036-wrapper-discovery.md)          | Accepted |
| 0037 | [Generated GitHub Pages gallery](0037-gallery-pages-site.md)                             | Accepted |
| 0038 | [Flat top-level CLI surface](0038-flat-cli-surface.md)                                  | Accepted |
| 0039 | [Composed gallery presets and a required-input coverage lint](0039-composed-gallery-presets.md) | Accepted |
| 0040 | [Automated gallery refresh on a schedule](0040-automated-gallery-refresh.md)                 | Accepted |
| 0041 | [An opinionated Juju page on the generated site](0041-juju-opinionated-gallery-page.md)    | Superseded by ADR-0057 |
| 0042 | [Multiple Juju pins, and modules that own their model](0042-juju-page-pins-and-own-model.md) | Accepted |
| 0043 | [The Juju page reads the environment, but guesses nothing](0043-juju-page-guesses-nothing.md) | Accepted |
| 0044 | [`--dir` names the wrapper; an existing wrapper is always the additive case](0044-dir-names-the-wrapper.md) | Accepted |
| 0045 | [A gallery entry offers presets as well as composing them](0045-gallery-available-presets.md) | Accepted |
| 0046 | [A gallery card names its required inputs once](0046-gallery-card-states-inputs-once.md) | Accepted |
| 0047 | [An import takes a gallery entry's module but none of its presets](0047-import-gallery-name.md) | Accepted |
| 0048 | [Machine-readable output behind `--json`](0048-machine-readable-output.md) | Accepted |
| 0049 | [The Juju page reads the model from the environment](0049-juju-page-model-from-environment.md) | Accepted |
| 0050 | [`atelier apply` converges on the block it finds](0050-tolerant-apply-converges.md) | Accepted |
| 0051 | [`atelier apply` exits 2 when Terraform failed](0051-apply-exit-codes.md) | Accepted |
| 0052 | [Hand the terminal to Terraform instead of re-rendering its output](0052-hand-the-terminal-to-terraform.md) | Accepted |
| 0053 | [The TUI does not list or apply preset bundles](0053-tui-does-not-list-or-apply-presets.md) | Accepted |
| 0054 | [A nested `map(string)` is edited as HCL lines](0054-nested-map-string-edits-as-hcl-lines.md) | Accepted |
| 0055 | [A gallery-sourced import names its own revision](0055-gallery-import-names-its-revision.md) | Accepted |
| 0056 | [Terraform action logs name the action and are bounded](0056-named-bounded-action-logs.md) | Accepted |
| 0058 | [The gallery admits first-party, non-Juju modules](0058-gallery-admits-non-juju-modules.md) | Accepted |
| 0057 | [One generated gallery page, with provider variants on each card](0057-single-gallery-page.md) | Accepted |
| 0059 | [Gallery candidate discovery is an on-demand tool](0059-gallery-candidate-discovery.md) | Accepted |

## Conventions

- Numbering is sequential; do not renumber.
- Status values: `Proposed`, `Accepted`, `Deprecated`, `Superseded by ADR-NNNN`.
- An ADR is immutable once `Accepted`. Changes are made by writing a new ADR
  that supersedes it.
- Cross-reference related ADRs by number; backlink from the superseded ADR if
  applicable.
