package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type behaviorFixture struct {
	Name         string                   `json:"name"`
	Source       string                   `json:"source"`
	EditorConfig string                   `json:"editorconfig"`
	Results      map[string]fixtureResult `json:"results"`
}

type fixtureResult struct {
	Formatted   string `json:"formatted"`
	Diagnostics string `json:"diagnostics"`
}

func TestCLI(t *testing.T) {
	zsh := requireTool(t, "zsh")
	requireTool(t, "shfmt")
	requireTool(t, "shellcheck")
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "zsh-lint")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, requireTool(t, "go"), "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	data, err := os.ReadFile("testdata/behavior.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []behaviorFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("behavior corpus is empty")
	}
	for _, backend := range []struct {
		name    string
		command []string
	}{
		{"go", []string{binary}},
		{"zsh", []string{zsh, "-f", filepath.Join(repo, "zsh-lint.zsh")}},
	} {
		t.Run(backend.name, func(t *testing.T) {
			runCLI := func(t *testing.T, dir, input string, args ...string) commandResult {
				t.Helper()
				return runTestCommand(t, dir, input, slices.Concat(backend.command, args)...)
			}
			for _, fixture := range fixtures {
				t.Run("corpus/"+fixture.Name, func(t *testing.T) {
					want, ok := fixture.Results[backend.name]
					if !ok {
						t.Fatalf("missing expectation for %s", backend.name)
					}
					dir := testWorkspace(t, fixture.EditorConfig)
					fixed := runCLI(t, dir, fixture.Source, "--fix", "--stdin-filename", "fixture.zsh", "-")
					assertCommandResult(t, fixed, commandResult{stdout: want.Formatted, stderr: want.Diagnostics})
					assertCommandResult(t, runCLI(t, dir, fixed.stdout, "--fix", "--stdin-filename", "fixture.zsh", "-"), fixed)
					code := 0
					if want.Diagnostics != "" {
						code = 1
					}
					assertCommandResult(t, runCLI(t, dir, fixed.stdout, "--stdin-filename", "fixture.zsh", "-"), commandResult{stdout: want.Diagnostics, code: code})
				})
			}
			for _, tc := range []struct {
				name string
				args []string
				err  string
			}{
				{"unknown", []string{"--unknown"}, "unknown option: --unknown"},
				{"missing value", []string{"--exclude"}, "option requires an argument: --exclude"},
				{"conflicting modes", []string{"--fix", "--diff"}, "--fix and --diff cannot be combined"},
				{"mixed stdin", []string{"-", "file.zsh"}, "- cannot be combined with other paths"},
				{"stdin name", []string{"--stdin-filename", "file.zsh"}, "--stdin-filename requires - as the path"},
			} {
				t.Run("usage/"+tc.name, func(t *testing.T) {
					want := "zsh-lint: " + tc.err + "\nTry 'zsh-lint --help' for more information.\n"
					assertCommandResult(t, runCLI(t, testWorkspace(t, ""), "", tc.args...), commandResult{stderr: want, code: 2})
				})
			}
			t.Run("help and version", func(t *testing.T) {
				dir := testWorkspace(t, "")
				for _, arg := range []string{"--help", "--version"} {
					got := runCLI(t, dir, "", arg)
					prefix := "Usage: zsh-lint"
					if arg == "--version" {
						prefix = "zsh-lint " + releaseVersion + " ("
					}
					if got.code != 0 || got.stderr != "" || !strings.HasPrefix(got.stdout, prefix) {
						t.Fatalf("%s: unexpected result: %+v", arg, got)
					}
				}
			})
			t.Run("stdin whitespace", func(t *testing.T) {
				dir := testWorkspace(t, "")
				assertCommandResult(t, runCLI(t, dir, "print ok", "-"), commandResult{stdout: "<stdin>: trailing whitespace or a missing final newline\n", code: 1})
				assertCommandResult(t, runCLI(t, dir, "print ok", "--fix", "-"), commandResult{stdout: "print ok\n"})
			})
			t.Run("syntax error", func(t *testing.T) {
				dir := testWorkspace(t, "")
				got := runCLI(t, dir, "if\n", "--stdin-filename", "broken.zsh", "-")
				if got.code != 1 || got.stderr != "" || !strings.Contains(got.stdout, "broken.zsh:") || !strings.Contains(got.stdout, "parse error") {
					t.Fatalf("unexpected syntax result: %+v", got)
				}
				fixed := runCLI(t, dir, "if\n", "--fix", "--stdin-filename", "broken.zsh", "-")
				assertCommandResult(t, fixed, commandResult{stdout: "if\n", stderr: got.stdout})
			})
			t.Run("discovery and explicit files", func(t *testing.T) {
				dir := testWorkspace(t, "[ignored.zsh]\nignore = true\n")
				for name, content := range map[string]string{
					"config.zsh": "print ok\n", ".zshrc": "print ok\n", "nested/function": "#autoload\nprint ok\n",
					".hidden/hidden.zsh": "if\n", "node_modules/module.zsh": "if\n", "vendor/plugin.zsh": "if\n",
					"ignored.zsh": "if\n", "bash.zsh": "#!/bin/bash\nexit 0\n", "autoload-file": "print ok\n",
				} {
					writeTestFile(t, dir, name, content)
				}
				assertCommandResult(t, runCLI(t, dir, "", "--exclude", "vendor", "."), commandResult{stdout: "Checked 3 files: 0 fixed, 0 not formattable, 0 problem(s).\n"})
				assertCommandResult(t, runCLI(t, dir, "", "autoload-file", "autoload-file"), commandResult{stdout: "Checked 1 files: 0 fixed, 0 not formattable, 0 problem(s).\n"})
				assertCommandResult(t, runCLI(t, dir, "", "bash.zsh"), commandResult{stdout: "Checked 0 files: 0 fixed, 0 not formattable, 0 problem(s).\n", stderr: "bash.zsh: not a zsh file, ignored\n"})
			})
			t.Run("dotted zsh shebang", func(t *testing.T) {
				dir := testWorkspace(t, "")
				writeTestFile(t, dir, "setup.sh", "#!/bin/zsh\nprint ok  \n")
				assertCommandResult(t, runCLI(t, dir, "", "."), commandResult{stdout: "setup.sh: trailing whitespace or a missing final newline\nChecked 1 files: 0 fixed, 0 not formattable, 1 problem(s).\n", code: 1})
			})
			t.Run("unreadable directory", func(t *testing.T) {
				if os.Geteuid() == 0 {
					t.Skip("root can read any directory")
				}
				dir := testWorkspace(t, "")
				writeTestFile(t, dir, "locked/inner.zsh", "print ok\n")
				writeTestFile(t, dir, "ok.zsh", "print ok  \n")
				if err := os.Chmod(filepath.Join(dir, "locked"), 0o000); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) })
				got := runCLI(t, dir, "", ".")
				want := "ok.zsh: trailing whitespace or a missing final newline\nChecked 1 files: 0 fixed, 0 not formattable, 2 problem(s).\n"
				if got.code != 1 || got.stdout != want || !strings.Contains(got.stderr, "permission denied") {
					t.Fatalf("unexpected result: %+v", got)
				}
			})
			t.Run("symlinked directory operand", func(t *testing.T) {
				dir := testWorkspace(t, "")
				writeTestFile(t, dir, "real/a.zsh", "print ok  \n")
				if err := os.Symlink("real", filepath.Join(dir, "link")); err != nil {
					t.Fatal(err)
				}
				assertCommandResult(t, runCLI(t, dir, "", "link"), commandResult{stdout: "link/a.zsh: trailing whitespace or a missing final newline\nChecked 1 files: 0 fixed, 0 not formattable, 1 problem(s).\n", code: 1})
			})
			t.Run("shebang interpreter", func(t *testing.T) {
				dir := testWorkspace(t, "")
				writeTestFile(t, dir, "tool.zsh", "#!/opt/zsh-tools/bin/bash\nexit 0\n")
				writeTestFile(t, dir, "bin/script", "#!/usr/bin/env -S zsh -f\nprint ok  \n")
				writeTestFile(t, dir, "crlf/script", "#!/usr/bin/env -u HOME zsh\r\nprint ok\n")
				assertCommandResult(t, runCLI(t, dir, "", "tool.zsh"), commandResult{stdout: "Checked 0 files: 0 fixed, 0 not formattable, 0 problem(s).\n", stderr: "tool.zsh: not a zsh file, ignored\n"})
				assertCommandResult(t, runCLI(t, dir, "", "bin"), commandResult{stdout: "bin/script: trailing whitespace or a missing final newline\nChecked 1 files: 0 fixed, 0 not formattable, 1 problem(s).\n", code: 1})
				got := runCLI(t, dir, "", "crlf")
				if got.stderr != "" || !strings.Contains(got.stdout, "Checked 1 files") {
					t.Fatalf("CRLF shebang not linted: %+v", got)
				}
			})
			t.Run("invalid exclude pattern", func(t *testing.T) {
				want := "zsh-lint: invalid --exclude pattern: [\nTry 'zsh-lint --help' for more information.\n"
				assertCommandResult(t, runCLI(t, testWorkspace(t, ""), "", "--exclude", "[", "."), commandResult{stderr: want, code: 2})
			})
			t.Run("duplicate operands", func(t *testing.T) {
				dir := testWorkspace(t, "")
				writeTestFile(t, dir, "a.zsh", "print ok\n")
				assertCommandResult(t, runCLI(t, dir, "", "a.zsh", "./a.zsh"), commandResult{stdout: "Checked 1 files: 0 fixed, 0 not formattable, 0 problem(s).\n"})
			})
			t.Run("file fix", func(t *testing.T) {
				dir := testWorkspace(t, "")
				path := writeTestFile(t, dir, "config.zsh", "print ok")
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
				assertCommandResult(t, runCLI(t, dir, "", "--fix", "config.zsh"), commandResult{stdout: "config.zsh: fixed\nChecked 1 files: 1 fixed, 0 not formattable, 0 problem(s).\n"})
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(content) != "print ok\n" || info.Mode().Perm() != 0o755 {
					t.Fatalf("file content=%q permissions=%v", content, info.Mode().Perm())
				}
				assertCommandResult(t, runCLI(t, dir, "", "--fix", "config.zsh"), commandResult{stdout: "Checked 1 files: 0 fixed, 0 not formattable, 0 problem(s).\n"})
			})
			t.Run("fix through a symlinked file", func(t *testing.T) {
				dir := testWorkspace(t, "")
				real := writeTestFile(t, dir, "real.zsh", "print ok  \n")
				link := filepath.Join(dir, "link.zsh")
				if err := os.Symlink("real.zsh", link); err != nil {
					t.Fatal(err)
				}
				assertCommandResult(t, runCLI(t, dir, "", "--fix", "link.zsh"), commandResult{stdout: "link.zsh: fixed\nChecked 1 files: 1 fixed, 0 not formattable, 0 problem(s).\n"})
				info, err := os.Lstat(link)
				if err != nil {
					t.Fatal(err)
				}
				content, err := os.ReadFile(real)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode()&os.ModeSymlink == 0 || string(content) != "print ok\n" {
					t.Fatalf("link mode=%v target content=%q", info.Mode(), content)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 3 {
					t.Fatalf("temporary files left behind: %v", entries)
				}
			})
			t.Run("fix keeps read-only files", func(t *testing.T) {
				if os.Geteuid() == 0 {
					t.Skip("root can write any file")
				}
				dir := testWorkspace(t, "")
				path := writeTestFile(t, dir, "ro.zsh", "print ok  \n")
				if err := os.Chmod(path, 0o444); err != nil {
					t.Fatal(err)
				}
				got := runCLI(t, dir, "", "--fix", "ro.zsh")
				if got.code != 1 || !strings.Contains(got.stdout, "ro.zsh: could not write the file") {
					t.Fatalf("unexpected result: %+v", got)
				}
				if content, err := os.ReadFile(path); err != nil || string(content) != "print ok  \n" {
					t.Fatalf("read-only file changed: %q %v", content, err)
				}
			})
			t.Run("fix keeps hard links", func(t *testing.T) {
				dir := testWorkspace(t, "")
				path := writeTestFile(t, dir, "a.zsh", "print ok  \n")
				other := filepath.Join(dir, "other")
				if err := os.Link(path, other); err != nil {
					t.Fatal(err)
				}
				assertCommandResult(t, runCLI(t, dir, "", "--fix", "a.zsh"), commandResult{stdout: "a.zsh: fixed\nChecked 1 files: 1 fixed, 0 not formattable, 0 problem(s).\n"})
				if content, err := os.ReadFile(other); err != nil || string(content) != "print ok\n" {
					t.Fatalf("hard link not updated: %q %v", content, err)
				}
			})
			t.Run("diff does not write", func(t *testing.T) {
				dir := testWorkspace(t, "")
				path := writeTestFile(t, dir, "config.zsh", "print ok")
				got := runCLI(t, dir, "", "--diff", "config.zsh")
				if got.code != 1 || got.stderr != "" {
					t.Fatalf("unexpected diff result: %+v", got)
				}
				for _, part := range []string{"--- config.zsh", "+++ config.zsh", "@@", "-print ok", "+print ok", "missing final newline"} {
					if !strings.Contains(got.stdout, part) {
						t.Fatalf("diff missing %q: %q", part, got.stdout)
					}
				}
				content, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(content) != "print ok" {
					t.Fatalf("diff changed file: %q", content)
				}
			})
			t.Run("shellcheck", func(t *testing.T) {
				dir := testWorkspace(t, "")
				got := runCLI(t, dir, "echo $value\n", "--shellcheck", "--stdin-filename", "fixture.zsh", "-")
				assertCommandResult(t, got, commandResult{})
				got = runCLI(t, dir, "cat file | grep pattern\n", "--shellcheck", "--stdin-filename", "fixture.zsh", "-")
				if got.code != 1 || got.stderr != "" || !strings.Contains(got.stdout, "fixture.zsh:1:") || !strings.Contains(got.stdout, "[SC2002]") {
					t.Fatalf("unexpected ShellCheck result: %+v", got)
				}
			})
		})
	}
}
