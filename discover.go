package main

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

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
	case strings.HasPrefix(line, "#compdef"), strings.HasPrefix(line, "#autoload"):
		return kindZsh
	case strings.HasPrefix(line, "#!"):
		if interpreter(line[2:]) == "zsh" {
			return kindZsh
		}
		return kindOther
	}
	return kindNone
}

// interpreter returns the program name a shebang runs, looking past env, its
// options and its variable assignments.
func interpreter(shebang string) string {
	fields := strings.Fields(shebang)
	if len(fields) == 0 {
		return ""
	}
	name := filepath.Base(fields[0])
	if name != "env" {
		return name
	}
	skip := false
	for _, field := range fields[1:] {
		switch {
		case skip:
			skip = false
		case field == "-u" || field == "-C" || field == "-P":
			skip = true
		case !strings.HasPrefix(field, "-") && !strings.Contains(field, "="):
			return filepath.Base(field)
		}
	}
	return ""
}

func isZshCandidate(path, name string) bool {
	switch lineKind(firstLine(path)) {
	case kindZsh:
		return true
	case kindOther:
		return false
	}
	zshName := strings.HasSuffix(name, ".zsh")
	if !zshName && !strings.HasPrefix(name, ".z") && strings.Contains(name, ".") {
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
	if info, err := os.Lstat(root); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		root += string(filepath.Separator)
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			fmt.Fprintf(os.Stderr, "zsh-lint: %v\n", err)
			l.problems++
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
		l.problems++
	}
}
