# zsh-lint

Linter and formatter for zsh files, built on the [mvdan.cc/sh](https://github.com/mvdan/sh) zsh parser.

- Syntax check with `zsh -n`
- Formatting that follows EditorConfig the way shfmt does, with a built-in style as the fallback
- Style checks for zsh pitfalls: literal quotes in defaults, spaced glob alternations, braced array references
- Subscripts keep their original spacing, so `[some-key]` never becomes `[some - key]`
- Optional ShellCheck pass, with the rules that misfire on zsh excluded

## Install

```sh
go install github.com/devastion/zsh-lint@latest
```

Requires `zsh` at runtime, and `shellcheck` for `--shellcheck`.

## Usage

```sh
zsh-lint                        # lint the current directory
zsh-lint -d ~/.config/zsh       # show what --fix would change
zsh-lint -f -x plugins .        # fix, skipping directories named plugins
zsh-lint functions/*            # autoload functions without a shebang
```

Run `zsh-lint --help` for all options and checks.

In a directory, a file counts as zsh when its first line is a zsh shebang, `#compdef` or `#autoload`, or when it has no shebang and is named `*.zsh` or after a zsh startup file. Hidden directories and `node_modules` are skipped.

## Formatting

When EditorConfig sets formatting properties for a file, they apply the way shfmt applies them. Use `[[zsh]]` sections to target zsh files without an extension:

```ini
[[zsh]]
indent_style = space
indent_size = 2
case_indent = true
space_redirects = true
```

Otherwise the built-in style applies: 2-space indent, indented case branches, spaced output redirections, tight input redirections and arithmetic negations.

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
