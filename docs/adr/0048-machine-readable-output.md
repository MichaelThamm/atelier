# ADR-0048: Machine-readable output behind `--json`

## Status

Accepted.

## Context

Every command in Atelier reports its result for a human: a `text/tabwriter`
table, a prose narrative, a unified diff. There is no `--json`, and the only
exit codes are `0` and `1`.

That is fine at a terminal and a problem everywhere else. A CI job or an
integration test that wants to know what `atelier import` did has to match
strings out of stderr:

```python
assert "Imported" in result.stderr
```

which passes whether one resource or four hundred were imported, breaks when
anyone rewords a sentence, and cannot see the difference between the two empty
runs that mean opposite things — *everything matched is already in state* (a
re-run) and *nothing matched at all* (usually a wrong model). The state of this
repository's own integration suite is the evidence: it asserts on prose and then
re-reads `terraform.tfstate` to find out what it actually wanted to know.

The cost is not hypothetical either. Atelier's Python test harness lives here,
so every consumer of the CLI is a second checkout to keep in step; and a
Python wrapper on PyPI, which is how a CI job should drive this tool, would have
to scrape the report too.

Two properties make a payload hard to add later rather than merely absent:

- **It is a contract with people this repository cannot see.** A field name
  published on PyPI is load-bearing for someone else's script.
- **Text output is genuinely good.** `atelier import` explains itself well,
  including why a resource was not imported. Replacing it with JSON would trade
  a working explanation for a structure nobody asked for.

## Decision

**A `--json` flag on the commands whose result is data. It replaces the result
and leaves everything else alone.**

- The payload goes to **stdout**, which is already the result channel
  (`atelier tidy`'s diff, `import --list`'s resource list). Progress, prompts,
  spinners and warnings stay on stderr, per the rule that `internal/` never
  writes to stdout and `cmd/atelier` only does for a result.
- **What happens to the text depends on what the text is.** Where it is a report
  — what `add` and `import` say about what they just did — it is still printed,
  to stderr: `--json` adds a second rendering and suppresses nothing, and the two
  cannot disagree because both are built from the same `Result`. Where the text
  *is* the result — the table `ls` and `wrappers` print — the payload replaces
  it, since two renderings cannot share stdout. Either way a run that failed
  still explains itself on stderr. `--json` output is indented, so a CI log is
  readable without `jq`.
- **Every payload is wrapped in one envelope**, so a consumer can tell what it
  got and which revision of the shape it is reading:

  ```json
  { "schema": 1, "command": "ls", "data": { } }
  ```

  `schema` changes when a field is removed or its meaning changes. Adding a
  field is not a change, so a consumer can ignore what it does not recognise.
- **Only data-bearing commands get it:** `add` (including `--list-var-files`),
  `ls`, `wrappers`, and `import` (including `--list`). Not `tidy`, whose payload
  *is* a diff; not `gallery`, whose `list --commands` and `requires` output is
  already one record per line; not `rm`, `purge` or `presets lint`, which report
  nothing to carry.
- **`atelier apply` rejects `--json`.** It reports Terraform's own output, which
  Atelier does not control. A silent no-op would be worse than the error: a CI
  job reads a missing payload as success.
- **Absence is `null`, empty collections are `[]`.** A consumer iterating a list
  should not have to handle `null`, and a consumer distinguishing "no ref" from
  "a field I do not know" needs `null` rather than an absent key.
- **A payload loses nothing the text report has.** `atelier ls` prints the
  repository and the ref and drops the `//subdir`; the payload carries `source`,
  `modulePath` and `ref` separately, which are exactly the three values
  `atelier add` takes. `import --json` reports every unmatched live object's
  name, where the text report shows three per type and elides long ones. A
  consumer that has to re-parse the text to recover a field has not been served.
- **Failures are unchanged.** A command that fails writes `atelier: <error>` to
  stderr and exits `1`; there is no error payload. A consumer reads stdout only
  on success.
- **A `--json` run that needs more input is a failure.** When a source matches
  several modules, `add` prints the candidates and exits `0`, because a person
  reads the list and re-runs with `--module`. Under `--json` the list moves to
  stderr and the exit code becomes `1`: stdout is the payload channel, so prose
  there is unparseable, and exiting `0` would report the one outcome a consumer
  cannot detect.
- **`add --json` reports the wrapper directory it wrote**, because `atelier add`
  creates a directory named after the module when it is not told where to write
  (ADR-0044), and "where did my wrapper go?" is otherwise unanswerable.
- **`add --json` reads the wrapper back** rather than echoing the request, so it
  reports the block name Atelier settled on — `--as cos-lite` is written as
  `cos_lite`, because the former is not a valid HCL identifier.

## Alternatives considered

- **Replace text output with JSON.** Rejected. The prose is the better
  explanation and most invocations are at a terminal; a flag lets each consumer
  take what it needs instead.
- **`--json` as `--format json`, for future formats.** Deferred. `--format` is
  the Juju spelling and would be the right one to add alongside a second format
  (`--format=json` is already how `juju show-model` works). One format does not
  need the indirection, and adding it later is not a breaking change.
- **Write the payload to a file (`--json <path>`).** Rejected: stdout is the
  result channel already, and a path invites a file that outlives the run.
- **Teach consumers to parse the state instead** (`terraform.tfstate`,
  `main.tf`). Rejected: that is a second, much larger contract, and it cannot
  answer the questions only the report answers — what was *matched*, what was
  *unresolved*, what a later apply would create.
- **`--json` on every command, including the ones with nothing to say.** Rejected
  as noise: a payload for "removed one block" is a shape with no reader.
- **Have the Python wrapper generate JSON itself.** Rejected. It would make the
  wrapper depend on the binary's exact wording — which is what the wrapper
  exists to stop depending on — and put a schema in the wrong repository.

## Consequences

- A CI job or test can read what a command did instead of matching prose, and can
  tell *nothing matched* from *everything is already in state*.
- `cmd/atelier/json_test.go` pins the exact bytes of every payload. These are
  contracts with consumers this repository cannot see, so a field added for a
  good reason must change an expectation deliberately.
- SPEC §6 records the payloads; `--help` names the flag on the four commands.
- `internal/` is unchanged. Encoding a CLI report is presentation, so the payload
  types live in `cmd/atelier` beside the commands that emit them.
- The schema is a promise from here on. A field may be added freely; removing
  one, or changing what one means, needs a new `schema` value and a new ADR.
