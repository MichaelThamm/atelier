package main

import (
	"fmt"
	"strings"
)

// flagValue reads the value of a `--name value` or `--name=value` flag found at
// args[i]. matched is false when args[i] names a different flag, so the caller's
// loop can fall through to its own handling. When the flag matches but its value
// is missing, err is non-nil and the caller should stop.
//
// It is the one place the two flag spellings are handled, so a parser bug or a
// new spelling (a `--name=` alias, say) is fixed for every command at once.
func flagValue(args []string, i int, name string) (value string, next int, matched bool, err error) {
	a := args[i]
	switch {
	case a == name:
		if i+1 >= len(args) {
			return "", i, true, fmt.Errorf("%s requires a value", name)
		}
		return args[i+1], i + 2, true, nil
	case strings.HasPrefix(a, name+"="):
		return strings.TrimPrefix(a, name+"="), i + 1, true, nil
	}
	return "", i, false, nil
}
