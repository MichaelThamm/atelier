// Package docscheck holds repository documentation-drift tests.
//
// It is repository tooling, not product code: the tests run as part of
// `go test ./...`, so `just check` and CI fail when the docs drift from the
// tree. The checks are intentionally limited to facts that are cheap to verify
// and expensive to notice by eye:
//
//   - every ADR file has exactly one index row, and the row links to the file
//     and agrees with the ADR's canonical status;
//   - every `ADR-NNNN` reference in tracked Markdown and Go resolves to an
//     existing ADR;
//   - every relative Markdown link resolves to a path that exists.
//
// They do not check prose, titles, or anchors: those are review concerns, and
// enforcing them here would generate false positives.
package docscheck
