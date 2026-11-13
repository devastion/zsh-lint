# zsh-lint

Linter and formatter for zsh files, built on the [mvdan.cc/sh](https://github.com/mvdan/sh) zsh parser.

- Syntax check with `zsh -n`
- Formatting that follows EditorConfig the way shfmt does, with a built-in style as the fallback
- Style checks for zsh pitfalls: literal quotes in defaults, spaced glob alternations, braced array references
- Subscripts keep their original spacing, so `[some-key]` never becomes `[some - key]`
- Optional ShellCheck pass, with the rules that misfire on zsh excluded
- A [pure zsh version](#pure-zsh-version) that uses shfmt and ShellCheck from `$PATH` and loads with plugin managers

## Install

```sh
go install github.com/devastion/zsh-lint@latest
```

Requires `zsh` at runtime, and `shellcheck` for `--shellcheck`.

For zsh completion, copy `_zsh-lint` into a directory on your `fpath`. The plugin below sets this up itself.

## Usage

```sh
zsh-lint                        # lint the current directory
zsh-lint -d ~/.config/zsh       # show what --fix would change
zsh-lint -f -x plugins .        # fix, skipping directories named plugins
zsh-lint functions/*            # autoload functions without a shebang
```

Run `zsh-lint --help` for all options and checks.

In a directory, a file counts as zsh when its first line is a shebang that runs zsh (directly or through `env`), `#compdef` or `#autoload`, or when it has no shebang and is named `*.zsh` or after a zsh startup file. The shebang is read before the name, so `setup.sh` with `#!/bin/zsh` counts. Hidden directories and `node_modules` are skipped. A directory that cannot be read is reported and counts as a problem (exit status 1), and the rest of the tree is still checked. An invalid `--exclude` pattern is a usage error (exit status 2).

## Formatting

When EditorConfig sets formatting properties for a file, they apply the way shfmt applies them. Use `[[zsh]]` sections to target zsh files without an extension:

```ini
[[zsh]]
indent_style = space
indent_size = 2
case_indent = true
space_redirects = true
```

Trailing blanks are removed only from code lines. Lines inside quoted strings and here-document bodies are left as they are, so `--fix` never changes their bytes. A blank escaped by a trailing backslash (`\ `) is kept, since removing it would join the next line onto the command. `--fix` refuses files that are not writable. It writes a temporary file next to the original and renames it over the file (or over the file a symlink points to), keeping its permission bits; ACLs and extended attributes are not copied. Files with other hard links or another owner are written in place instead. Files that do not parse keep their trailing blanks, and the Go backend's parser decides which lines are code. The pure zsh backend uses a line scanner instead, and leaves a file unchanged when it cannot finish scanning.

Otherwise the built-in style applies: 2-space indent, indented case branches, spaced output redirections, tight input redirections and arithmetic negations.

## Pure zsh version

`zsh-lint.zsh` runs without Go. It formats with [shfmt](https://github.com/mvdan/sh) and checks with ShellCheck, both taken from `$PATH`. It needs zsh and a shfmt with the zsh dialect (tested with zsh 5.9 and shfmt 3.14). ShellCheck is only needed for `--shellcheck`, and `diff` for `--diff`.

Run the script directly:

```sh
./zsh-lint.zsh -d ~/.config/zsh
```

Or load `zsh-lint.plugin.zsh` with a plugin manager, which defines a `zsh-lint` shell function:

```zsh
# zinit
zinit light devastion/zsh-lint

# antidote, in .zsh_plugins.txt
devastion/zsh-lint

# Oh My Zsh, after cloning into $ZSH_CUSTOM/plugins/zsh-lint
plugins=(... zsh-lint)

# without a plugin manager
source /path/to/zsh-lint/zsh-lint.plugin.zsh
```

For sheldon, add to `plugins.toml`:

```toml
[plugins.zsh-lint]
github = "devastion/zsh-lint"
```

The plugin also adds the `_zsh-lint` completion, which works for the binary too. It registers the completion even when it's loaded after `compinit`.

The function runs in a subshell, so it doesn't change options or variables in your shell. In an interactive shell it shadows a `zsh-lint` binary on `$PATH`; `command zsh-lint` runs the binary. Editors start their own process and can't see the function, so point them at `zsh-lint.zsh` or the binary.

It takes the same options and prints the same messages as the Go binary, with these differences:

- Formatting comes from shfmt and EditorConfig only. Without EditorConfig formatting properties, shfmt's defaults apply (tab indentation) instead of the built-in style. shfmt reads `switch_case_indent`, not `case_indent`.
- Files are parsed as zsh unless EditorConfig sets `language_dialect`.
- The style checks match lines of text instead of the syntax tree, so comments and here-document bodies can trigger them. There is no layout check.
- `--diff` uses `diff -u`, which can group hunks differently.
- `-x` patterns are zsh globs, so `(a|b)` alternation works.
- Empty files stay empty.
- It is slower. On a 410-file zsh config it took 18 seconds against 6 for the binary, because each file starts several shfmt and zsh processes.

## Editor integration

Neovim with [conform.nvim](https://github.com/stevearc/conform.nvim) and [nvim-lint](https://github.com/mfussenegger/nvim-lint):

```lua
require('conform').setup({
  formatters = {
    zsh_lint = {
      command = 'zsh-lint',
      args = { '--fix', '--stdin-filename', '$FILENAME', '-' },
      stdin = true,
    },
  },
  formatters_by_ft = { zsh = { 'zsh_lint' } },
})

require('lint').linters.zsh_lint = {
  cmd = 'zsh-lint',
  stdin = true,
  args = { '--stdin-filename', function() return vim.api.nvim_buf_get_name(0) end, '-' },
  stream = 'stdout',
  ignore_exitcode = true,
  parser = require('lint.parser').from_pattern('^[^:]+:(%d+): (.+)$', { 'lnum', 'message' }),
}
require('lint').linters_by_ft = { zsh = { 'zsh_lint' } }
```

## License

[MIT](LICENSE)
