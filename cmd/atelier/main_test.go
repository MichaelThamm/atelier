package main

import (
	"strings"
	"testing"
)

func TestParseInitArgs_valid(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want func(*testing.T, parsedFields)
	}{
		{
			name: "positional URL",
			args: []string{"https://example.com/m.git"},
			want: func(t *testing.T, p parsedFields) {
				if p.Source != "https://example.com/m.git" {
					t.Errorf("Source = %q", p.Source)
				}
				if p.LocalSource {
					t.Errorf("LocalSource should be false")
				}
			},
		},
		{
			name: "URL with ref and module",
			args: []string{"https://example.com/m.git", "--ref", "v1.2.0", "--module", "terraform/cos"},
			want: func(t *testing.T, p parsedFields) {
				if p.Source != "https://example.com/m.git" || p.Ref != "v1.2.0" || p.ModulePath != "terraform/cos" {
					t.Errorf("parsed = %+v", p)
				}
			},
		},
		{
			name: "--source path",
			args: []string{"--source", "./terraform/cos-lite"},
			want: func(t *testing.T, p parsedFields) {
				if p.Source != "./terraform/cos-lite" || !p.LocalSource {
					t.Errorf("parsed = %+v", p)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts, err := parseInitArgs(c.args)
			if err != nil {
				t.Fatal(err)
			}
			c.want(t, parsedFields{
				Source:      opts.Source,
				Ref:         opts.Ref,
				ModulePath:  opts.ModulePath,
				LocalSource: opts.LocalSource,
			})
		})
	}
}

func TestParseInitArgs_errors(t *testing.T) {
	cases := []struct {
		name, want string
		args       []string
	}{
		{"--source no value", "--source requires", []string{"--source"}},
		{"--ref no value", "--ref requires", []string{"https://x.git", "--ref"}},
		{"--module no value", "--module requires", []string{"https://x.git", "--module"}},
		{"URL plus --source", "cannot combine", []string{"https://x.git", "--source", "./y"}},
		{"two positional URLs", "exactly one URL argument", []string{"https://a.git", "https://b.git"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseInitArgs(c.args)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q; expected to contain %q", err.Error(), c.want)
			}
		})
	}
}

type parsedFields struct {
	Source      string
	Ref         string
	ModulePath  string
	LocalSource bool
}
