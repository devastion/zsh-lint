package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlugin(t *testing.T) {
	zsh := requireTool(t, "zsh")
	repo, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, timing := range []string{"before", "after"} {
		t.Run("completion "+timing+" compinit", func(t *testing.T) {
			dir := testWorkspace(t, "")
			script := `
setopt no_unset
repo=$1
local -a original_fpath=("$fpath[@]")
if [[ $2 == after ]]; then
  autoload -Uz compinit
  compinit -u -D || exit 1
fi
alias lint='false'
alias report='false'
source "$repo/zsh-lint.plugin.zsh" || exit 2
[[ -o aliases && -o nounset && ! -o extendedglob ]] || exit 3
(( $+functions[zsh-lint] && ! $+functions[lint] && ! $+functions[report] )) || exit 4
(( ${fpath[(Ie)$repo]} )) || exit 5
if [[ $2 == before ]]; then
  autoload -Uz compinit
  compinit -u -D || exit 6
fi
[[ $_comps[zsh-lint] == _zsh-lint ]] || exit 7
zsh-lint --help > /dev/null || exit 8
[[ -o aliases && -o nounset && ! -o extendedglob ]] || exit 9
(( ! $+functions[lint] && ! $+functions[report] && ! $+parameters[problems] )) || exit 10
[[ $aliases[lint] == false && $aliases[report] == false ]] || exit 11
source "$repo/zsh-lint.plugin.zsh" || exit 12
local -a matches=("${(@M)fpath:#$repo}")
(( $#matches == 1 )) || exit 13
print -r -- ok
`
			assertCommandResult(t, runTestCommand(t, dir, "", zsh, "-f", "-c", script, "plugin-test", repo, timing), commandResult{stdout: "ok\n"})
		})
	}
	t.Run("load from a function", func(t *testing.T) {
		dir := testWorkspace(t, "")
		script := `
load_plugin() { source "$1"; }
load_plugin "$1" || exit 1
(( $+functions[zsh-lint] )) || exit 2
zsh-lint --help > /dev/null || exit 3
print -r -- ok
`
		assertCommandResult(t, runTestCommand(t, dir, "", zsh, "-f", "-c", script, "plugin-test", filepath.Join(repo, "zsh-lint.plugin.zsh")), commandResult{stdout: "ok\n"})
	})
}
