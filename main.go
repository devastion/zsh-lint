// Command zsh-lint lints and formats zsh files.
package main

import (
	"errors"
	"fmt"
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

Exit status: 0 when clean, 1 when problems were found, 2 on usage errors, 127
when a required tool is missing. With --fix and -, problems go to stderr and
do not affect the exit status.
`

// releaseVersion is the release both backends report; zsh-lint.zsh repeats it
// and TestZshBackendConstants keeps the two equal. Bump it before tagging.
const releaseVersion = "v0.1.0"

type options struct {
	fix, diff, verbose, shellcheck, help, version bool
	excludes                                      []string
	stdinName                                     string
	operands                                      []string
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

	for _, pattern := range o.excludes {
		if _, err := filepath.Match(pattern, ""); err != nil {
			return o, fmt.Errorf("invalid --exclude pattern: %s", pattern)
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

// version names the release, shared with zsh-lint.zsh, and adds the module
// version the binary was built from when it differs, such as a commit build.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "zsh-lint " + releaseVersion + " (unknown build)"
	}
	var details []string
	if build := info.Main.Version; build != "" && build != "(devel)" && build != releaseVersion {
		details = append(details, "build "+build)
	}
	for _, dep := range info.Deps {
		if dep.Path == "mvdan.cc/sh/v3" {
			details = append(details, "mvdan.cc/sh/v3 "+dep.Version)
		}
	}
	if len(details) == 0 {
		details = append(details, "unknown build")
	}
	return "zsh-lint " + releaseVersion + " (" + strings.Join(details, ", ") + ")"
}
