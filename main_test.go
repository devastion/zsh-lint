package main

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

func TestLineKind(t *testing.T) {
	cases := []struct {
		line string
		want kind
	}{
		{"#!/bin/zsh", kindZsh},
		{"#!/usr/bin/env zsh", kindZsh},
		{"#compdef command", kindZsh},
		{"#autoload", kindZsh},
		{"#!/bin/bash", kindOther},
		{"#!/usr/bin/env python3", kindOther},
		{"#! /bin/zsh -f", kindZsh},
		{"#!/hint/zsh", kindZsh},
		{"#!/usr/bin/env -S zsh -f", kindZsh},
		{"#!/usr/bin/env LC_ALL=C zsh", kindZsh},
		{"#!/usr/bin/env -u HOME zsh", kindZsh},
		{"#!/bin/zsh\r", kindZsh},
		{"#!/opt/zsh-tools/bin/bash", kindOther},
		{"#!/bin/zshell", kindOther},
		{"#!/usr/bin/env", kindOther},
		{"#!", kindOther},
		{"# ordinary comment", kindNone},
		{"", kindNone},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			if got := lineKind(tc.line); got != tc.want {
				t.Fatalf("lineKind(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestDiscover(t *testing.T) {
	dir := testWorkspace(t, "[ignored.zsh]\nignore = true\n")
	files := map[string]string{
		".zshrc":                  "print ok\n",
		"config.zsh":              "print ok\n",
		"nested/function":         "#autoload\nprint ok\n",
		"nested/_completion":      "#compdef command\n",
		"nested/script":           "#!/usr/bin/env zsh\nprint ok\n",
		"nested/readme.txt":       "plain text\n",
		"bash.zsh":                "#!/bin/bash\nexit 0\n",
		"ignored.zsh":             "print ok\n",
		".hidden/hidden.zsh":      "print ok\n",
		"node_modules/module.zsh": "print ok\n",
		"vendor/plugin.zsh":       "print ok\n",
		"backup.bak.zsh":          "print ok\n",
	}
	for name, content := range files {
		writeTestFile(t, dir, name, content)
	}
	if err := os.Symlink(filepath.Join(dir, "config.zsh"), filepath.Join(dir, "link.zsh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nested"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	l := newLinter(options{excludes: []string{"vendor", "*.bak.zsh"}})
	var got []string
	l.discover(dir, func(path string) {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, filepath.ToSlash(rel))
	})
	want := []string{".zshrc", "config.zsh", "nested/_completion", "nested/function", "nested/script"}
	if !slices.Equal(got, want) {
		t.Fatalf("discovered = %q, want %q", got, want)
	}
}

func TestFirstLine(t *testing.T) {
	for _, src := range []string{"", "#autoload", "#!/bin/zsh\nprint ok\n", strings.Repeat("x", 300)} {
		t.Run(src[:min(len(src), 20)], func(t *testing.T) {
			path := writeTestFile(t, t.TempDir(), "file", src)
			want, _, _ := strings.Cut(src[:min(len(src), 256)], "\n")
			if got := firstLine(path); got != want {
				t.Fatalf("firstLine = %q, want %q", got, want)
			}
		})
	}
}
