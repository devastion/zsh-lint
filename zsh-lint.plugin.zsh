# zsh-lint plugin: defines the zsh-lint function.
#
# Finds its own directory per the Zsh Plugin Standard, so it works when a
# plugin manager sources it by any path or from inside a function.
0=${${ZERO:-${0:#$ZSH_ARGZERO}}:-${(%):-%N}}
0=${${(M)0:#/*}:-$PWD/$0}

# Aliases expand while a file is parsed. Sourcing with no_aliases keeps
# aliases such as `alias lint=...` out of the definitions, without changing
# the caller's options or adding a helper function.
emulate zsh -o no_aliases -c 'source "${0:h}/zsh-lint.zsh"'
