package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/rogpeppe/go-internal/diff"
	"mvdan.cc/editorconfig"
	"mvdan.cc/sh/v3/syntax"
)

const shellcheckExcludes = "SC1087,SC2034,SC2053,SC2068,SC2076,SC2086,SC2128,SC2154," +
	"SC2190,SC2206,SC2207,SC2295,SC2296,SC2298,SC2299,SC2300,SC2301"

const (
	msgQuotedDefault     = "single quotes in a default inside double quotes stay literal"
	msgSpacedAlternation = "spaces inside a glob alternation become part of the pattern"
	msgBracedArray       = `braced array reference; write "$name[@]"`
)

var (
	defaultOperators = map[syntax.ParExpOperator]bool{
		syntax.DefaultUnset: true, syntax.DefaultUnsetOrNull: true,
		syntax.AlternateUnset: true, syntax.AlternateUnsetOrNull: true,
		syntax.AssignUnset: true, syntax.AssignUnsetOrNull: true,
		syntax.ErrorUnset: true, syntax.ErrorUnsetOrNull: true,
	}

	bracedArray       = regexp.MustCompile(`^"\$\{[[:alnum:]_]+\[@\]\}"$`)
	spacedAlternation = regexp.MustCompile(`\([^()\s]+ \| [^()]+\)`)

	lineQuotedDefault     = regexp.MustCompile(`"\$\{[[:alnum:]_]+:-'`)
	lineSpacedAlternation = regexp.MustCompile(`\[\[ .*\([^()\s]+ \| [^()]+\).* \]\]`)
	lineBracedArray       = regexp.MustCompile(`"\$\{[[:alnum:]_]+\[@\]\}"`)

	heredocStart  = regexp.MustCompile(`(^|[^<])<<-?[ \t]*["'\\]?[A-Za-z_][^\s;&|<>()"']*`)
	heredocQuotes = strings.NewReplacer(`"`, "", `'`, "", `\`, "")
)

type linter struct {
	opts                                options
	out                                 io.Writer
	parser                              *syntax.Parser
	editorConfig                        editorconfig.Query
	problems, fixed, skipped, unchecked int
}

func newLinter(o options) *linter {
	return &linter{
		opts:   o,
		out:    os.Stdout,
		parser: syntax.NewParser(syntax.KeepComments(true), syntax.Variant(syntax.LangZsh)),
		editorConfig: editorconfig.Query{
			FileCache:   map[string]*editorconfig.File{},
			RegexpCache: map[string]*regexp.Regexp{},
		},
	}
}

func (l *linter) parse(name string, content []byte) (*syntax.File, error) {
	return l.parser.Parse(bytes.NewReader(content), name)
}

func (l *linter) report(text string) {
	fmt.Fprintln(l.out, text)
	l.problems++
}

func (l *linter) problem(name string, line int, message string) {
	if line > 0 {
		l.report(fmt.Sprintf("%s:%d: %s", name, line, message))
		return
	}
	l.report(name + ": " + message)
}

func (l *linter) skip(name, reason string) {
	l.skipped++
	if l.opts.verbose {
		fmt.Fprintf(l.out, "%s: not formattable: %s\n", name, reason)
	}
}

func (l *linter) ignored(path string) bool {
	props, err := l.editorConfig.Find(absolute(path), editorConfigLangs)
	return err == nil && props.Get("ignore") == "true"
}

// lint checks one file's content and returns the content --fix leaves.
func (l *linter) lint(name, configPath string, src []byte) []byte {
	cleaned := clean(src)
	result := formatResult{content: cleaned}

	valid := l.checkSyntax(name, src)
	if valid {
		result = l.format(name, configPath, cleaned)
	}
	l.reportChanges(name, src, cleaned, result.content)
	if !valid {
		return result.content
	}

	// Reports refer to the lines of the content that ends up on disk.
	checked := src
	if l.opts.fix {
		checked = result.content
	}
	l.checkStyle(name, checked)
	if result.house && !result.trusted {
		l.checkLayout(name, checked)
	}
	if l.opts.shellcheck {
		l.runShellCheck(name, checked)
	}
	return result.content
}

func (l *linter) reportChanges(name string, src, cleaned, final []byte) {
	if l.opts.fix || bytes.Equal(src, final) {
		return
	}
	if l.opts.diff {
		l.out.Write(diff.Diff(name, src, name, final))
	}
	if bytes.Equal(cleaned, final) {
		l.problem(name, 0, "trailing whitespace or a missing final newline")
	} else {
		l.problem(name, 0, "not formatted; run zsh-lint --fix")
	}
}

func zshCheck(content []byte) (string, bool) {
	cmd := exec.Command("zsh", "-fn")
	cmd.Stdin = bytes.NewReader(content)
	output, err := cmd.CombinedOutput()
	message := strings.TrimSpace(string(output))
	return message, err == nil && message == ""
}

func (l *linter) checkSyntax(name string, content []byte) bool {
	message, ok := zshCheck(content)
	if ok {
		return true
	}
	if message == "" {
		l.problem(name, 0, "syntax error")
		return false
	}
	for _, line := range strings.Split(message, "\n") {
		if rest, found := strings.CutPrefix(line, "zsh:"); found {
			l.report(name + ":" + rest)
		} else {
			l.report(name + ": " + line)
		}
	}
	return false
}

func (l *linter) checkStyle(name string, content []byte) {
	file, err := l.parse(name, content)
	if err != nil {
		l.checkStyleText(name, content)
		return
	}

	text := func(n syntax.Node) string {
		s, ok := span(content, n)
		if !ok {
			return ""
		}
		return s.text
	}
	line := func(n syntax.Node) int { return int(n.Pos().Line()) }

	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.DblQuoted:
			if bracedArray.MatchString(text(n)) {
				l.problem(name, line(n), msgBracedArray)
			}
			for _, part := range n.Parts {
				p, ok := part.(*syntax.ParamExp)
				if ok && p.Exp != nil && p.Exp.Word != nil && defaultOperators[p.Exp.Op] &&
					strings.Contains(text(p.Exp.Word), "'") {
					l.problem(name, line(p), msgQuotedDefault)
				}
			}
		case *syntax.TestClause:
			if spacedAlternation.MatchString(text(n)) {
				l.problem(name, line(n), msgSpacedAlternation)
			}
		}
		return true
	})
}

func (l *linter) checkStyleText(name string, content []byte) {
	for i, line := range splitLines(content) {
		if lineQuotedDefault.MatchString(line) {
			l.problem(name, i+1, msgQuotedDefault)
		}
		if lineSpacedAlternation.MatchString(line) {
			l.problem(name, i+1, msgSpacedAlternation)
		}
		if lineBracedArray.MatchString(line) {
			l.problem(name, i+1, msgBracedArray)
		}
	}
}

// checkLayout covers files the formatter could not handle. Here-document
// bodies are skipped, since tabs and redirection-like text there are content.
func (l *linter) checkLayout(name string, content []byte) {
	delimiter, strip := "", false
	for i, line := range splitLines(content) {
		number := i + 1
		if delimiter != "" {
			body := line
			if strip {
				body = strings.TrimLeft(body, "\t")
			}
			if body == delimiter {
				delimiter = ""
			}
			continue
		}

		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		if strings.Contains(indent, "\t") {
			l.problem(name, number, "tab indentation")
		}
		if strings.Contains(line, "$(< ") || strings.Contains(line, "<<< ") {
			l.problem(name, number, "space after an input redirection")
		}

		match := heredocStart.FindStringIndex(line)
		if match == nil || strings.Contains(line[:match[0]+1], "((") {
			continue
		}
		word := line[match[0]:match[1]]
		strip = strings.Contains(word, "<<-")
		word = word[strings.Index(word, "<<")+2:]
		word = strings.TrimLeft(strings.TrimPrefix(word, "-"), " \t")
		delimiter = heredocQuotes.Replace(word)
	}
}

func (l *linter) runShellCheck(name string, content []byte) {
	cmd := exec.Command("shellcheck", "--shell=bash", "--format=gcc", "--exclude="+shellcheckExcludes, "-")
	cmd.Stdin = bytes.NewReader(content)
	output, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		l.problem(name, 0, "could not run shellcheck: "+err.Error())
		return
	}

	text := strings.TrimSpace(string(output))
	if text == "" {
		return
	}
	findings := strings.Split(text, "\n")
	for _, finding := range findings {
		if strings.Contains(finding, ": error: ") && strings.Contains(finding, "[SC1") {
			l.unchecked++
			if l.opts.verbose {
				fmt.Fprintf(l.out, "%s: not shellchecked: ShellCheck cannot parse it\n", name)
			}
			return
		}
	}
	for _, finding := range findings {
		if rest, ok := strings.CutPrefix(finding, "-:"); ok {
			finding = name + ":" + rest
		}
		l.report(finding)
	}
}

// clean strips trailing blanks from code lines and trailing blank lines from
// the end, and ends the content with exactly one newline. A line whose newline
// lies inside a quoted string or a here-document body keeps its blanks, a blank
// escaped by a backslash is kept, and content that does not parse is returned
// unchanged.
func clean(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangZsh)).Parse(bytes.NewReader(src), "")
	if err != nil {
		return src
	}
	protected := literalRanges(file)

	lines := strings.Split(string(src), "\n")
	offset, next := 0, 0
	for i, line := range lines {
		end := offset + len(line)
		for next < len(protected) && protected[next][1] <= uint(end) {
			next++
		}
		if next == len(protected) || protected[next][0] > uint(end) {
			lines[i] = trimBlanks(line)
		}
		offset = end + 1
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// trimBlanks strips trailing spaces and tabs, except one escaped by a
// backslash: removing it would join the next line onto this one.
func trimBlanks(line string) string {
	trimmed := strings.TrimRight(line, " \t")
	backslashes := len(trimmed) - len(strings.TrimRight(trimmed, `\`))
	if backslashes%2 == 1 && len(trimmed) < len(line) {
		return line[:len(trimmed)+1]
	}
	return trimmed
}

// literalRanges returns the byte ranges whose text is data rather than code,
// quoted strings and here-document bodies, sorted and merged.
func literalRanges(file *syntax.File) [][2]uint {
	var ranges [][2]uint
	add := func(n syntax.Node) {
		ranges = append(ranges, [2]uint{n.Pos().Offset(), n.End().Offset()})
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			add(n)
		case *syntax.Redirect:
			if n.Hdoc != nil {
				add(n.Hdoc)
			}
		}
		return true
	})
	slices.SortFunc(ranges, func(a, b [2]uint) int { return cmp.Compare(a[0], b[0]) })
	var merged [][2]uint
	for _, r := range ranges {
		if last := len(merged) - 1; last >= 0 && r[0] <= merged[last][1] {
			merged[last][1] = max(merged[last][1], r[1])
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

func splitLines(content []byte) []string {
	return strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
}
