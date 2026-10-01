package main

import "testing"

func TestResolveCommand(t *testing.T) {
	cases := []struct {
		args []string
		want command
		rest []string
	}{
		{nil, cmdOpen, nil},
		{[]string{"module"}, cmdModuleOther, nil},
		{[]string{"module", "add", "u"}, cmdModuleOther, []string{"add", "u"}},
		{[]string{"module", "rm", "n"}, cmdModuleOther, []string{"rm", "n"}},
		// The alias and the canonical spelling route to the same command with
		// the same remaining arguments.
		{[]string{"module", "apply", "u"}, cmdModuleApply, []string{"u"}},
		{[]string{"apply", "u"}, cmdModuleApply, []string{"u"}},
		{[]string{"apply"}, cmdModuleApply, nil},
		{[]string{"purge"}, cmdPurge, nil},
		{[]string{"tidy", "--write"}, cmdTidy, []string{"--write"}},
		{[]string{"import", "juju"}, cmdImport, []string{"juju"}},
		{[]string{"presets", "lint", "--module", "m"}, cmdPresets, []string{"lint", "--module", "m"}},
		{[]string{"gallery", "list"}, cmdGallery, []string{"list"}},
		{[]string{"nonsense"}, command("nonsense"), nil},
	}
	for _, c := range cases {
		got, rest := resolveCommand(c.args)
		if got != c.want {
			t.Errorf("resolveCommand(%v) = %q, want %q", c.args, got, c.want)
		}
		if !equalStrings(rest, c.rest) {
			t.Errorf("resolveCommand(%v) rest = %v, want %v", c.args, rest, c.rest)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
