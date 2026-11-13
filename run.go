package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (l *linter) runPaths() int {
	operands := l.opts.operands
	if len(operands) == 0 {
		operands = []string{"."}
	}

	var files []string
	seen := map[string]bool{}
	add := func(path string) {
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			files = append(files, path)
		}
	}

	for _, operand := range operands {
		info, err := os.Stat(operand)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "zsh-lint: %v\n", err)
			l.problems++
		case info.IsDir():
			l.discover(operand, add)
		case !info.Mode().IsRegular():
			fmt.Fprintf(os.Stderr, "%s: not a regular file, ignored\n", operand)
		case lineKind(firstLine(operand)) == kindOther:
			fmt.Fprintf(os.Stderr, "%s: not a zsh file, ignored\n", operand)
		default:
			add(operand)
		}
	}

	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			l.problem(path, 0, err.Error())
			continue
		}
		final := l.lint(path, path, src)
		if !l.opts.fix || bytes.Equal(src, final) {
			continue
		}
		if err := writeFile(path, final); err != nil {
			l.problem(path, 0, "could not write the file: "+err.Error())
			continue
		}
		l.fixed++
		fmt.Fprintf(l.out, "%s: fixed\n", path)
	}

	summary := fmt.Sprintf("Checked %d files: %d fixed, %d not formattable", len(files), l.fixed, l.skipped)
	if l.opts.shellcheck {
		summary += fmt.Sprintf(", %d not shellchecked", l.unchecked)
	}
	if l.skipped+l.unchecked > 0 && !l.opts.verbose {
		summary += " (use -v to list them)"
	}
	fmt.Fprintf(l.out, "%s, %d problem(s).\n", summary, l.problems)

	if l.problems > 0 {
		return 1
	}
	return 0
}

func (l *linter) runStdin() int {
	src, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zsh-lint: %v\n", err)
		return 1
	}

	name, configPath := l.opts.stdinName, l.opts.stdinName
	if name == "" {
		name, configPath = "<stdin>", "stdin"
	}
	if l.opts.fix {
		l.out = os.Stderr
	}

	final := l.lint(name, configPath, src)

	if l.opts.fix {
		if _, err := os.Stdout.Write(final); err != nil {
			return 1
		}
		return 0
	}
	if l.problems > 0 {
		return 1
	}
	return 0
}
