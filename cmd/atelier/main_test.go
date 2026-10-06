package main

import "testing"

func TestResolveCommand(t *testing.T) {
	cases := []struct {
		args []string
		want command
		rest []string
	}{
		{nil, cmdOpen, nil},
		{[]string{"add", "u"}, cmdAdd, []string{"u"}},
		{[]string{"rm", "n"}, cmdRm, []string{"n"}},
		{[]string{"ls"}, cmdLs, nil},
		{[]string{"list"}, cmdLs, nil},
		{[]string{"apply", "u"}, cmdApply, []string{"u"}},
		{[]string{"apply"}, cmdApply, nil},
		{[]string{"wrappers"}, cmdWrappers, nil},
		{[]string{"wrappers", "tf-testing"}, cmdWrappers, []string{"tf-testing"}},
		{[]string{"purge"}, cmdPurge, nil},
		{[]string{"import", "juju"}, cmdImport, []string{"juju"}},
		{[]string{"presets", "lint", "--module", "m"}, cmdPresets, []string{"lint", "--module", "m"}},
		{[]string{"gallery", "list"}, cmdGallery, []string{"list"}},
		// The removed `module` namespace is not an alias; it falls through as an
		// unknown command carrying its remaining args.
		{[]string{"module", "add", "u"}, command("module"), []string{"add", "u"}},
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
