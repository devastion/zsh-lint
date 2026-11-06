package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var editorConfigLangs = []string{"shell", "zsh"}

// formattingKeys are the EditorConfig properties shfmt maps onto the printer,
// with older spellings after the current one.
var formattingKeys = [][]string{
	{"indent_style"},
	{"indent_size"},
	{"binary_next_line"},
	{"case_indent", "switch_case_indent"},
	{"space_redirects"},
	{"function_next_line"},
	{"simplify"},
	{"minify"},
}

var inputRedirects = map[syntax.RedirOperator]int{
	syntax.RdrIn:    len("<"),
	syntax.WordHdoc: len("<<<"),
}

type formatResult struct {
	content []byte
	house   bool // the built-in style applies
	trusted bool // content is verified formatter output
}

type edit struct {
	start, end int
	text       string
}

type sourceSpan struct {
	start, end int
	text       string
}

func absolute(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func (l *linter) format(name, configPath string, src []byte) formatResult {
	printer, house, simplify := l.printerFor(configPath)
	result := formatResult{content: src, house: house}

	file, err := l.parse(name, src)
	if err != nil {
		l.skip(name, "the zsh parser cannot parse it: "+err.Error())
		return result
	}
	if simplify {
		syntax.Simplify(file)
	}

	var buf bytes.Buffer
	if err := printer.Print(&buf, file); err != nil {
		l.skip(name, "printing failed: "+err.Error())
		return result
	}
	out := buf.Bytes()

	outFile, err := l.parse(name, out)
	if err != nil || !slices.Equal(shape(file), shape(outFile)) {
		l.skip(name, "formatting would change the program structure")
		return result
	}

	edits, ok := restoreIndexes(file, src, outFile, out)
	if !ok {
		l.skip(name, "formatting would change subscripts")
		return result
	}
	if house {
		edits = append(edits, tightenings(outFile, out)...)
	}
	out = applyEdits(out, edits)

	if !bytes.Equal(out, src) {
		if _, err := l.parse(name, out); err != nil {
			l.skip(name, "the formatted result does not parse")
			return result
		}
		if _, ok := zshCheck(out); !ok {
			l.skip(name, "the formatted result is not valid zsh")
			return result
		}
	}

	result.content = out
	result.trusted = true
	return result
}

// printerFor applies the EditorConfig properties that shfmt honors, or the
// built-in style when none of them are set for the file.
func (l *linter) printerFor(path string) (printer *syntax.Printer, house, simplify bool) {
	props, err := l.editorConfig.Find(absolute(path), editorConfigLangs)
	if err == nil {
		get := func(names ...string) (string, bool) {
			for _, name := range names {
				if prop := props.Lookup(name); prop != nil {
					return prop.Value, true
				}
			}
			return "", false
		}
		set := slices.ContainsFunc(formattingKeys, func(names []string) bool {
			_, ok := get(names...)
			return ok
		})
		if set {
			enabled := func(names ...string) bool {
				value, _ := get(names...)
				return value == "true"
			}
			indent := uint(0)
			if style, _ := get("indent_style"); style == "space" {
				indent = 8
				if size := props.IndentSize(); size > 0 {
					indent = uint(size)
				}
			}
			minify := enabled("minify")
			return syntax.NewPrinter(
				syntax.Indent(indent),
				syntax.BinaryNextLine(enabled("binary_next_line")),
				syntax.SwitchCaseIndent(enabled("case_indent", "switch_case_indent")),
				syntax.SpaceRedirects(enabled("space_redirects")),
				syntax.FunctionNextLine(enabled("function_next_line")),
				syntax.Minify(minify),
			), false, minify || enabled("simplify")
		}
	}

	return syntax.NewPrinter(
		syntax.Indent(2),
		syntax.SwitchCaseIndent(true),
		syntax.SpaceRedirects(true),
	), true, false
}

// shape lists the node types in walk order; formatting must not change it.
func shape(f *syntax.File) []string {
	var types []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if n != nil {
			types = append(types, fmt.Sprintf("%T", n))
		}
		return true
	})
	return types
}

func indexes(f *syntax.File) []syntax.ArithmExpr {
	var list []syntax.ArithmExpr
	syntax.Walk(f, func(node syntax.Node) bool {
		var index syntax.ArithmExpr
		switch n := node.(type) {
		case *syntax.ParamExp:
			index = n.Index
		case *syntax.Assign:
			index = n.Index
		case *syntax.ArrayElem:
			index = n.Index
		}
		if index != nil {
			list = append(list, index)
		}
		return true
	})
	return list
}

// restoreIndexes keeps every subscript as written in the source when the
// printer only changed its spacing, so [some-key] never becomes [some - key].
func restoreIndexes(srcFile *syntax.File, src []byte, outFile *syntax.File, out []byte) ([]edit, bool) {
	from, to := indexes(srcFile), indexes(outFile)
	if len(from) != len(to) {
		return nil, false
	}

	var edits []edit
	for i := range from {
		was, ok := span(src, from[i])
		if !ok {
			continue
		}
		now, ok := span(out, to[i])
		if !ok || was.text == now.text {
			continue
		}
		if strings.Contains(was.text+now.text, "\n") || squeeze(was.text) != squeeze(now.text) {
			continue
		}
		edits = append(edits, edit{start: now.start, end: now.end, text: was.text})
	}
	return edits, true
}

// tightenings removes the space after input redirections and arithmetic
// negations, which the built-in style keeps tight.
func tightenings(f *syntax.File, out []byte) []edit {
	var edits []edit
	gap := func(start, end int) {
		if start < end && end <= len(out) && strings.Trim(string(out[start:end]), " \t") == "" {
			edits = append(edits, edit{start: start, end: end})
		}
	}

	syntax.Walk(f, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.Redirect:
			length, ok := inputRedirects[n.Op]
			target := n.Word
			if target == nil {
				target = n.Hdoc
			}
			if ok && target != nil {
				gap(int(n.OpPos.Offset())+length, int(target.Pos().Offset()))
			}
		case *syntax.UnaryArithm:
			if n.Op == syntax.Not && !n.Post {
				gap(int(n.OpPos.Offset())+1, int(n.X.Pos().Offset()))
			}
		}
		return true
	})
	return edits
}

// applyEdits applies non-overlapping edits; earlier edits win over later ones.
func applyEdits(b []byte, edits []edit) []byte {
	var accepted []edit
	for _, e := range edits {
		overlaps := slices.ContainsFunc(accepted, func(a edit) bool {
			return e.start < a.end && a.start < e.end
		})
		if !overlaps {
			accepted = append(accepted, e)
		}
	}
	slices.SortFunc(accepted, func(a, b edit) int { return b.start - a.start })

	out := slices.Clone(b)
	for _, e := range accepted {
		out = slices.Concat(out[:e.start], []byte(e.text), out[e.end:])
	}
	return out
}

func span(b []byte, n syntax.Node) (sourceSpan, bool) {
	if !n.Pos().IsValid() || !n.End().IsValid() {
		return sourceSpan{}, false
	}
	start, end := int(n.Pos().Offset()), int(n.End().Offset())
	if start > end || end > len(b) {
		return sourceSpan{}, false
	}
	return sourceSpan{start: start, end: end, text: string(b[start:end])}, true
}

func squeeze(s string) string {
	return strings.Join(strings.Fields(s), "")
}
