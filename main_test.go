package main

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want options
		err  string
	}{
		{name: "defaults"},
		{name: "flags", args: []string{"-f", "-v", "--shellcheck", "file.zsh"}, want: options{fix: true, verbose: true, shellcheck: true, operands: []string{"file.zsh"}}},
		{name: "long flags", args: []string{"--diff", "--verbose", "."}, want: options{diff: true, verbose: true, operands: []string{"."}}},
		{name: "stdin", args: []string{"--fix", "--stdin-filename", "config.zsh", "-"}, want: options{fix: true, stdinName: "config.zsh", operands: []string{"-"}}},
		{name: "equals", args: []string{"--exclude=vendor", "--stdin-filename=config.zsh", "-"}, want: options{excludes: []string{"vendor"}, stdinName: "config.zsh", operands: []string{"-"}}},
		{name: "repeated exclusions", args: []string{"-x", "vendor", "--exclude", "*.bak", "src"}, want: options{excludes: []string{"vendor", "*.bak"}, operands: []string{"src"}}},
		{name: "end of options", args: []string{"--", "-file.zsh"}, want: options{operands: []string{"-file.zsh"}}},
		{name: "help", args: []string{"-h"}, want: options{help: true}},
		{name: "version", args: []string{"--version"}, want: options{version: true}},
		{name: "unknown option", args: []string{"--unknown"}, err: "unknown option: --unknown"},
		{name: "missing exclusion", args: []string{"-x"}, err: "option requires an argument: -x"},
		{name: "missing stdin name", args: []string{"--stdin-filename"}, err: "option requires an argument: --stdin-filename"},
		{name: "conflicting modes", args: []string{"-f", "-d"}, err: "--fix and --diff cannot be combined"},
		{name: "mixed stdin", args: []string{"-", "file.zsh"}, err: "- cannot be combined with other paths"},
		{name: "repeated stdin", args: []string{"-", "-"}, err: "- cannot be combined with other paths"},
		{name: "stdin name without stdin", args: []string{"--stdin-filename=name"}, err: "--stdin-filename requires - as the path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseArgs(tc.args)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("options = %#v, want %#v", got, tc.want)
			}
		})
	}
}
