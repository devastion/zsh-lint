// Command zsh-lint lints and formats zsh files.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
)

const usage = `Usage: zsh-lint [options] [path...]
       zsh-lint [options] [--stdin-filename NAME] -

Lint and format zsh files. Each path is a file or a directory; directories are
searched recursively. Without paths, the current directory is searched. With -,
the content is read from standard input.

In a directory, a file counts as zsh when its first line is a zsh shebang,
#compdef or #autoload, or when it has no shebang and is named *.zsh or after a
zsh startup file (.zshrc, zshenv, ...). Hidden directories, node_modules and
files with "ignore = true" in EditorConfig are skipped. Files named explicitly
are linted unless their shebang names another interpreter.

Options:
  -f, --fix                  Rewrite files that need whitespace or formatting
                             fixes; with -, print the result to stdout
  -d, --diff                 Show what --fix would change as a unified diff
  -x, --exclude NAME         Skip files and directories whose name matches the
                             glob NAME; repeatable
      --shellcheck           Also run ShellCheck in bash mode, with the rules
                             that misfire on zsh excluded
      --stdin-filename NAME  Name for the content read from -, used in reports
                             and for the EditorConfig lookup
  -v, --verbose              List the files that cannot be formatted or
                             shellchecked, with the reason
  -V, --version              Show the version
  -h, --help                 Show this help

Formatting:
  When EditorConfig sets formatting properties for a file (indent_style,
  indent_size, binary_next_line, case_indent, space_redirects,
  function_next_line, simplify, minify), they apply the way shfmt applies
  them. language_dialect is ignored; files are always parsed as zsh.
  Otherwise the built-in style applies: 2-space indent, indented case
  branches, spaced output redirections, tight input redirections and
  arithmetic negations. Subscripts keep their original spacing.

Checks:
  syntax       every file must parse with zsh -n
  style        literal quotes in defaults, spaced glob alternations, braced
               array references
  layout       tab indentation and spaced input redirections outside
               here-documents; built-in style, unformattable files only
  whitespace   trailing whitespace and the final newline
  format       the mvdan.cc/sh formatter
  shellcheck   with --shellcheck; files ShellCheck cannot parse are skipped

Exit status: 0 when clean, 1 when problems were found, 2 on usage errors. With
--fix and -, problems go to stderr and do not affect the exit status.
`

type options struct {
	fix, diff, verbose, shellcheck, help, version bool
	excludes                                      []string
	stdinName                                     string
	operands                                      []string
}

type kind int

const (
	kindNone kind = iota
	kindZsh
	kindOther
)

var startupNames = map[string]bool{
	".zshenv": true, ".zprofile": true, ".zshrc": true, ".zlogin": true, ".zlogout": true,
	"zshenv": true, "zprofile": true, "zshrc": true, "zlogin": true, "zlogout": true,
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zsh-lint: %v\nTry 'zsh-lint --help' for more information.\n", err)
		return 2
	}
	if o.help {
		fmt.Print(usage)
		return 0
	}
	if o.version {
		fmt.Println(version())
		return 0
	}

	tools := []string{"zsh"}
	if o.shellcheck {
		tools = append(tools, "shellcheck")
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			fmt.Fprintf(os.Stderr, "zsh-lint: %s is required\n", tool)
			return 127
		}
	}

	l := newLinter(o)
	if slices.Contains(o.operands, "-") {
		return l.runStdin()
	}
	return l.runPaths()
}

func parseArgs(args []string) (options, error) {
	var o options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(arg, "=")
		switch {
		case arg == "-f" || arg == "--fix":
			o.fix = true
		case arg == "-d" || arg == "--diff":
			o.diff = true
		case arg == "-v" || arg == "--verbose":
			o.verbose = true
		case arg == "--shellcheck":
			o.shellcheck = true
		case arg == "-h" || arg == "--help":
			o.help = true
		case arg == "-V" || arg == "--version":
			o.version = true
		case name == "--exclude" && hasValue:
			o.excludes = append(o.excludes, value)
		case name == "--stdin-filename" && hasValue:
			o.stdinName = value
		case arg == "-x" || arg == "--exclude" || arg == "--stdin-filename":
			if i+1 == len(args) {
				return o, fmt.Errorf("option requires an argument: %s", arg)
			}
			i++
			if arg == "--stdin-filename" {
				o.stdinName = args[i]
			} else {
				o.excludes = append(o.excludes, args[i])
			}
		case arg == "--":
			o.operands = append(o.operands, args[i+1:]...)
			i = len(args)
		case len(arg) > 1 && arg[0] == '-':
			return o, fmt.Errorf("unknown option: %s", arg)
		default:
			o.operands = append(o.operands, arg)
		}
	}

	stdin := slices.Contains(o.operands, "-")
	switch {
	case o.fix && o.diff:
		return o, errors.New("--fix and --diff cannot be combined")
	case stdin && len(o.operands) > 1:
		return o, errors.New("- cannot be combined with other paths")
	case o.stdinName != "" && !stdin:
		return o, errors.New("--stdin-filename requires - as the path")
	}
	return o, nil
}

func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "zsh-lint (unknown version)"
	}
	v := "zsh-lint " + info.Main.Version
	for _, dep := range info.Deps {
		if dep.Path == "mvdan.cc/sh/v3" {
			v += " (mvdan.cc/sh/v3 " + dep.Version + ")"
		}
	}
	return v
}

func firstLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 256)
	n, _ := io.ReadFull(f, buf)
	line, _, _ := bytes.Cut(buf[:n], []byte("\n"))
	return string(line)
}

func lineKind(line string) kind {
	switch {
	case strings.HasPrefix(line, "#!") && strings.Contains(line, "zsh"),
		strings.HasPrefix(line, "#compdef"),
		strings.HasPrefix(line, "#autoload"):
		return kindZsh
	case strings.HasPrefix(line, "#!"):
		return kindOther
	}
	return kindNone
}

func isZshCandidate(path, name string) bool {
	zshName := strings.HasSuffix(name, ".zsh")
	if !zshName && !strings.HasPrefix(name, ".z") && strings.Contains(name, ".") {
		return false
	}
	switch lineKind(firstLine(path)) {
	case kindZsh:
		return true
	case kindOther:
		return false
	}
	return zshName || startupNames[name]
}

func (l *linter) excluded(name string, dir bool) bool {
	if dir && (strings.HasPrefix(name, ".") || name == "node_modules") {
		return true
	}
	return slices.ContainsFunc(l.opts.excludes, func(pattern string) bool {
		matched, _ := filepath.Match(pattern, name)
		return matched
	})
}

func (l *linter) discover(root string, add func(string)) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "zsh-lint: %v\n", err)
			return nil
		}
		if path != root && l.excluded(d.Name(), d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && isZshCandidate(path, d.Name()) && !l.ignored(path) {
			add(path)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "zsh-lint: %v\n", err)
	}
}

func (l *linter) runPaths() int {
	operands := l.opts.operands
	if len(operands) == 0 {
		operands = []string{"."}
	}

	var files []string
	seen := map[string]bool{}
	add := func(path string) {
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
		if err := os.WriteFile(path, final, 0o644); err != nil {
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
