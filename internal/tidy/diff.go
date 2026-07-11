package tidy

import "strings"

// lineDiff renders a minimal unified-style diff between old and new, line by
// line. Removed lines are prefixed "- ", added lines "+ ", and unchanged
// context lines "  ". tidy's prune is almost entirely removals, but additions
// are handled too so reflows (e.g. a collapsed object) read correctly.
//
// The algorithm is a standard longest-common-subsequence over lines — small
// inputs (a single module block), so the O(n·m) table is fine and keeps the
// output deterministic and dependency-free.
func lineDiff(old, new string) string {
	a := splitLines(old)
	b := splitLines(new)

	// LCS length table.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var sb strings.Builder
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			sb.WriteString("  " + a[i] + "\n")
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			sb.WriteString("- " + a[i] + "\n")
			i++
		default:
			sb.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	for ; i < len(a); i++ {
		sb.WriteString("- " + a[i] + "\n")
	}
	for ; j < len(b); j++ {
		sb.WriteString("+ " + b[j] + "\n")
	}
	return sb.String()
}

// splitLines splits s into lines, dropping a single trailing empty line so a
// file ending in "\n" doesn't yield a spurious blank diff line.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}
