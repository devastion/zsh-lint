# zsh-lint plugin: defines the zsh-lint function and its completion.
#
# Finds its own directory per the Zsh Plugin Standard, so it works when a
# plugin manager sources it by any path or from inside a function.
0=${${ZERO:-${0:#$ZSH_ARGZERO}}:-${(%):-%N}}
0=${${(M)0:#/*}:-$PWD/$0}

# Aliases expand while a file is parsed. Sourcing with no_aliases keeps
# aliases such as `alias lint=...` out of the definitions, without changing
# the caller's options or adding a helper function.
emulate zsh -o no_aliases -c 'source "${0:h}/zsh-lint.zsh"'

# The completion (_zsh-lint) sits next to this file. compinit finds it on fpath;
# when compinit has already run, as with deferred plugin loading, register it.
((${fpath[(Ie)${0:h}]})) || fpath+=("${0:h}")
if ((${+functions[compdef]})); then
  autoload -Uz _zsh-lint
  compdef _zsh-lint zsh-lint
fi
