package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCleanOrdinaryWhitespace(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"empty", "", ""},
		{"clean", "print ok\n", "print ok\n"},
		{"missing newline", "print ok", "print ok\n"},
		{"trailing blanks", "print ok \t\n", "print ok\n"},
		{"trailing blank lines", "print ok\n\n\n", "print ok\n"},
		{"internal blank line", "print one\n\nprint two\n", "print one\n\nprint two\n"},
		{"unicode space", "print ok\u00a0\n", "print ok\u00a0\n"},
		{"here document body", "cat <<EOF\nkeep   \nEOF\nprint ok  \n", "cat <<EOF\nkeep   \nEOF\nprint ok\n"},
		{"here document terminator line", "cat <<EOF\nbody\nEOF   \nEOF\n", "cat <<EOF\nbody\nEOF   \nEOF\n"},
		{"tab stripped here document body", "cat <<-EOF\n\tkeep \n\tEOF\n", "cat <<-EOF\n\tkeep \n\tEOF\n"},
		{"single quoted newline", "print 'a  \nb'  \n", "print 'a  \nb'\n"},
		{"double quoted newline", "print \"a  \nb\"\n", "print \"a  \nb\"\n"},
		{"unparsable unchanged", "if true; then  \n", "if true; then  \n"},
		{"escaped trailing blank", "print a \\ \nprint b\n", "print a \\ \nprint b\n"},
		{"escaped blank then blanks", "print a \\  \t\n", "print a \\ \n"},
		{"escaped trailing tab", "print a \\\t\n", "print a \\\t\n"},
		{"escaped backslash", "print a \\\\  \n", "print a \\\\\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(clean([]byte(tc.src))); got != tc.want {
				t.Fatalf("clean = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestZshBackendConstants(t *testing.T) {
	script, err := os.ReadFile("zsh-lint.zsh")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"version":                releaseVersion,
		"shellcheck_excludes":    shellcheckExcludes,
		"msg_quoted_default":     "'" + msgQuotedDefault + "'",
		"msg_spaced_alternation": "'" + msgSpacedAlternation + "'",
		"msg_braced_array":       "'" + msgBracedArray + "'",
	} {
		match := regexp.MustCompile(`(?m)^\s*local ` + name + `=(.*)$`).FindSubmatch(script)
		if match == nil || string(match[1]) != want {
			t.Errorf("zsh-lint.zsh %s = %q, want %q", name, match, want)
		}
	}
}

func TestStyle(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"quoted default", "print -r -- \"${value:-'fallback'}\"\n", []string{msgQuotedDefault}},
		{"braced array", "print -r -- \"${items[@]}\"\n", []string{msgBracedArray}},
		{"spaced alternation", "[[ $value == (foo | bar) ]]\n", []string{msgSpacedAlternation}},
		{"unbraced array", "print -r -- \"$items[@]\"\n", nil},
		{"unquoted default", "print -r -- ${value:-'fallback'}\n", nil},
		{"comment", "# print -r -- \"${items[@]}\"\n", nil},
		{"here document", "cat <<'EOF'\n\"${items[@]}\"\nEOF\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLinter(options{})
			var out bytes.Buffer
			l.out = &out
			if _, err := l.parse("style.zsh", []byte(tc.src)); err != nil {
				t.Fatalf("fixture must use AST checks: %v", err)
			}
			l.checkStyle("style.zsh", []byte(tc.src))
			var want strings.Builder
			for _, message := range tc.want {
				fmt.Fprintf(&want, "style.zsh:1: %s\n", message)
			}
			if out.String() != want.String() || l.problems != len(tc.want) {
				t.Fatalf("diagnostics = %q (%d problems), want %q (%d problems)", &out, l.problems, want.String(), len(tc.want))
			}
		})
	}
}

func TestLayoutSkipsHereDocuments(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"tabs", "\tprint ok\n", "layout.zsh:1: tab indentation\n"},
		{"input redirection", "print $(< file)\n", "layout.zsh:1: space after an input redirection\n"},
		{"quoted delimiter", "cat <<'EOF'\n\t$(< file)\nEOF\n", ""},
		{"tab stripped delimiter", "cat <<-EOF\n\t$(< file)\n\tEOF\n", ""},
		{"after here document", "cat <<EOF\n\ttext\nEOF\n\tprint ok\n", "layout.zsh:4: tab indentation\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLinter(options{})
			var out bytes.Buffer
			l.out = &out
			l.checkLayout("layout.zsh", []byte(tc.src))
			if out.String() != tc.want || l.problems != strings.Count(tc.want, "\n") {
				t.Fatalf("diagnostics = %q (%d problems), want %q", &out, l.problems, tc.want)
			}
		})
	}
}

func TestReportChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    options
		src     string
		cleaned string
		final   string
		want    string
	}{
		{name: "unchanged", src: "print ok\n", cleaned: "print ok\n", final: "print ok\n"},
		{name: "whitespace", src: "print ok", cleaned: "print ok\n", final: "print ok\n", want: "file.zsh: trailing whitespace or a missing final newline\n"},
		{name: "format", src: "if true;then :;fi\n", cleaned: "if true;then :;fi\n", final: "if true; then\n  :\nfi\n", want: "file.zsh: not formatted; run zsh-lint --fix\n"},
		{name: "fix", opts: options{fix: true}, src: "print ok", cleaned: "print ok\n", final: "print ok\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLinter(tc.opts)
			var out bytes.Buffer
			l.out = &out
			l.reportChanges("file.zsh", []byte(tc.src), []byte(tc.cleaned), []byte(tc.final))
			if out.String() != tc.want || l.problems != strings.Count(tc.want, "\n") {
				t.Fatalf("diagnostics = %q (%d problems), want %q", &out, l.problems, tc.want)
			}
		})
	}
}
