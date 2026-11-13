package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type commandResult struct {
	stdout string
	stderr string
	code   int
}

func requireTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required for integration tests: %v", name, err)
	}
	return path
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func testWorkspace(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, ".editorconfig", "root = true\n"+config)
	return dir
}

func runTestCommand(t *testing.T, dir, input string, args ...string) commandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "ZDOTDIR="+dir, "LC_ALL=C")
	cmd.Stdin = bytes.NewBufferString(input)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("command %q timed out: %v\nstdout: %s\nstderr: %s", args, ctx.Err(), &stdout, &stderr)
	}
	result := commandResult{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("command %q failed: %v", args, err)
		}
		result.code = exitErr.ExitCode()
	}
	return result
}

func assertCommandResult(t *testing.T, got, want commandResult) {
	t.Helper()
	if got != want {
		t.Fatalf("command result:\nexit: %d (want %d)\nstdout: %q (want %q)\nstderr: %q (want %q)",
			got.code, want.code, got.stdout, want.stdout, got.stderr, want.stderr)
	}
}
