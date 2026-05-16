package tui

import "regexp"

// ansiRE matches CSI escape sequences. We use it in tests where assertions
// run against rendered output but don't care about the styling — only that
// the text content is right. Tests that specifically *want* to verify
// styling (theme_test.go) skip this helper and look for "\x1b[" directly.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI removes all CSI escape sequences from a string.
func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}
