package main

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestFormat(t *testing.T) {
	requireTool(t, "zsh")
	cases := []struct {
		name   string
		config string
		src    string
		want   string
		house  bool
	}{
		{"built in", "", "if true;then\nprint ok >out\nfi\n", "if true; then\n  print ok > out\nfi\n", true},
		{"editorconfig", "[*]\nindent_style = space\nindent_size = 4\n", "if true;then\nprint ok >out\nfi\n", "if true; then\n    print ok >out\nfi\n", false},
		{"language section", "[[zsh]]\nindent_style = space\nindent_size = 3\n", "if true;then\nprint ok\nfi\n", "if true; then\n   print ok\nfi\n", false},
		{"non formatting properties", "[*]\ncharset = utf-8\n", "if true;then\nprint ok\nfi\n", "if true; then\n  print ok\nfi\n", true},
		{"nested subscript", "", "typeset -A values\nvalues[some-key]=ok\nprint -r -- ${values[some-key]}\n", "typeset -A values\nvalues[some-key]=ok\nprint -r -- ${values[some-key]}\n", true},
		{"tight input", "", "print $(< file)\ncat <<< value\n(( ! flag ))\n", "print $(<file)\ncat <<<value\n((!flag))\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := testWorkspace(t, tc.config)
			l := newLinter(options{})
			var diagnostics bytes.Buffer
			l.out = &diagnostics
			path := filepath.Join(dir, "fixture.zsh")
			got := l.format("fixture.zsh", path, []byte(tc.src))
			if string(got.content) != tc.want || got.house != tc.house || !got.trusted || l.skipped != 0 {
				t.Fatalf("format = %q (house=%v trusted=%v skipped=%d), want %q (house=%v)\ndiagnostics: %s",
					got.content, got.house, got.trusted, l.skipped, tc.want, tc.house, &diagnostics)
			}
			again := l.format("fixture.zsh", path, got.content)
			if !bytes.Equal(again.content, got.content) || !again.trusted {
				t.Fatalf("format is not idempotent: first %q, second %q", got.content, again.content)
			}
		})
	}
}

func TestEditorConfigPrecedence(t *testing.T) {
	dir := testWorkspace(t, "[*]\nindent_style = space\nindent_size = 2\n")
	writeTestFile(t, dir, "nested/.editorconfig", "[*.zsh]\nindent_size = 4\n")
	l := newLinter(options{})
	for _, tc := range []struct {
		path string
		want string
	}{
		{"file.zsh", "if true; then\n  print ok\nfi\n"},
		{"nested/file.zsh", "if true; then\n    print ok\nfi\n"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			printer, house, simplify := l.printerFor(filepath.Join(dir, tc.path))
			if house || simplify {
				t.Fatalf("unexpected defaults: house=%v simplify=%v", house, simplify)
			}
			file, err := l.parse(tc.path, []byte("if true; then\nprint ok\nfi\n"))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := printer.Print(&out, file); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("printed = %q, want %q", &out, tc.want)
			}
		})
	}
}

func TestApplyEdits(t *testing.T) {
	cases := []struct {
		name  string
		edits []edit
		want  string
	}{
		{"none", nil, "abcdef"},
		{"unordered", []edit{{start: 4, end: 6, text: "!"}, {start: 0, end: 1, text: "XYZ"}}, "XYZbcd!"},
		{"adjacent", []edit{{start: 0, end: 2, text: "A"}, {start: 2, end: 4, text: "B"}}, "ABef"},
		{"overlap", []edit{{start: 1, end: 4, text: "X"}, {start: 2, end: 5, text: "Y"}}, "aXef"},
		{"insertion", []edit{{start: 6, end: 6, text: "!"}}, "abcdef!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte("abcdef")
			if got := string(applyEdits(src, tc.edits)); got != tc.want {
				t.Fatalf("applyEdits = %q, want %q", got, tc.want)
			}
			if string(src) != "abcdef" {
				t.Fatalf("source was modified: %q", src)
			}
		})
	}
}
