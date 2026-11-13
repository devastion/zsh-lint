#!/usr/bin/env zsh
#
# zsh-lint: lint and format zsh files in zsh, with shfmt and shellcheck from
# $PATH. Source this file to define the zsh-lint function, or run it directly.
#
# Requires: zsh and shfmt with the zsh dialect (tested with zsh 5.9 and
# shfmt 3.14); shellcheck for --shellcheck.

# The body is a subshell, so options, helper functions, traps and exit never
# leak into the calling shell.
zsh-lint() (
  emulate -L zsh
  setopt extended_glob no_unset pipe_fail warn_create_global
  zmodload zsh/mapfile zsh/system || return 2
  zmodload -F zsh/stat b:zstat || return 2

  local version=v0.1.0
  local shellcheck_excludes=SC1087,SC2034,SC2053,SC2068,SC2076,SC2086,SC2128,SC2154,SC2190,SC2206,SC2207,SC2295,SC2296,SC2298,SC2299,SC2300,SC2301
  local msg_quoted_default='single quotes in a default inside double quotes stay literal'
  local msg_spaced_alternation='spaces inside a glob alternation become part of the pattern'
  local msg_braced_array='braced array reference; write "$name[@]"'
  local re_quoted_default='"\$\{[[:alnum:]_]+:-'"'"
  local re_spaced_alternation='\[\[ .*\([^()[:space:]]+ \| [^()]+\).* \]\]'
  local re_braced_array='"\$\{[[:alnum:]_]+\[@\]\}"'
  # One character before the bracket, so test brackets ([[ ]]) and globs are
  # not taken for subscripts.
  local re_subscript='[[:alnum:]_]\[[^][]*\]'

  local -A startup_names=(
    .zshenv 1 .zprofile 1 .zshrc 1 .zlogin 1 .zlogout 1
    zshenv 1 zprofile 1 zshrc 1 zlogin 1 zlogout 1
  )

  local -i opt_fix=0 opt_diff=0 opt_verbose=0 opt_shellcheck=0 opt_help=0 opt_version=0
  local -i problems=0 fixed=0 skipped=0 unchecked=0 out_fd=1
  local -a excludes operands files
  local -A seen
  local stdin_name='' REPLY='' MATCH='' nl=$'\n' blanks=$' \t'
  local -i MBEGIN MEND
  local -a reply match mbegin mend

  local usage=''
  IFS= read -r -d '' usage << 'EOF'
Usage: zsh-lint [options] [path...]
       zsh-lint [options] [--stdin-filename NAME] -

Lint and format zsh files with shfmt and shellcheck from $PATH. Each path is a
file or a directory; directories are searched recursively. Without paths, the
current directory is searched. With -, the content is read from standard input.

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
  shfmt formats every file with the EditorConfig properties it reads
  (indent_style, indent_size, binary_next_line, switch_case_indent,
  space_redirects, function_next_line, simplify, minify); without them,
  shfmt's defaults apply. Files are parsed as zsh, unless EditorConfig sets
  language_dialect. Subscripts keep their original spacing.

Checks:
  syntax       every file must parse with zsh -n
  style        literal quotes in defaults, spaced glob alternations, braced
               array references
  whitespace   trailing whitespace and the final newline
  format       shfmt
  shellcheck   with --shellcheck; files ShellCheck cannot parse are skipped

Exit status: 0 when clean, 1 when problems were found, 2 on usage errors, 127
when a required tool is missing. With --fix and -, problems go to stderr and
do not affect the exit status.
EOF

  # --- reporting -------------------------------------------------------------

  report() {
    print -ru$out_fd -- "$1"
    ((problems++))
  }

  problem() { # name line message
    if (($2 > 0)); then
      report "$1:$2: $3"
    else
      report "$1: $3"
    fi
  }

  skip() { # name reason
    ((skipped++))
    ((opt_verbose)) && print -ru$out_fd -- "$1: not formattable: $2"
    return 0
  }

  usage_error() {
    print -ru2 -- "zsh-lint: $1"
    print -ru2 -- "Try 'zsh-lint --help' for more information."
    exit 2
  }

  # --- text helpers ----------------------------------------------------------

  # Lines of $1 without the final newline, into reply.
  split_lines() {
    reply=("${(@ps:\n:)${1%$nl}}")
  }

  # clean strips trailing blanks from code lines and trailing blank lines from
  # the end, and ends the content with exactly one newline. A line whose newline
  # lies inside a quoted string or a here-document body keeps its blanks, and a
  # blank escaped by a backslash is kept. This is a line scanner, not a parser:
  # if it cannot finish with every string and here-document closed, the content
  # is returned unchanged.
  clean() {
    if [[ -z $1 ]]; then
      REPLY=''
      return
    fi
    local -a lines=("${(@ps:\n:)1}") literal=() pending=() chars
    local line c word body want tab=$'\t' q='' closing esc
    local -i j n strip lit ok=1
    for line in "$lines[@]"; do
      if (($#pending)); then
        body=$line
        want=${pending[1]#*:}
        if [[ ${pending[1]%%:*} == 1 ]]; then
          while [[ $body == $tab* ]]; do body=${body#$tab}; done
        fi
        [[ $body == "$want" ]] && pending[1]=()
        literal+=(1)
        continue
      fi
      chars=("${(@s::)line}")
      n=$#chars
      for ((j = 1; j <= n; j++)); do
        c=$chars[j]
        if [[ -n $q ]]; then
          if [[ $c == '\' && $esc == 1 ]]; then
            ((j++))
          elif [[ $c == $closing ]]; then
            q=''
          fi
          continue
        fi
        case $c in
          '\') ((j++)) ;;
          "'")
            q="'" closing="'" esc=0
            ((j > 1)) && [[ $chars[j-1] == '$' ]] && esc=1
            ;;
          '"') q='"' closing='"' esc=1 ;;
          '#')
            # Not after (, which starts glob flags such as (#i).
            if ((j == 1)) || [[ $chars[j-1] == [$blanks\;\&\|] ]]; then
              break
            fi
            ;;
          '<')
            if [[ $chars[j+1] == '<' && $chars[j+2] != '<' ]] &&
              [[ $chars[j-1] != '<' ]] && [[ ${line[1,j]} != *'(('* ]]; then
              ((j += 2))
              strip=0
              if [[ $chars[j] == '-' ]]; then
                strip=1
                ((j++))
              fi
              while [[ $chars[j] == [$blanks] ]]; do ((j++)); done
              word=''
              while ((j <= n)) && [[ $chars[j] != [$blanks\;\&\|\<\>\(\)] ]]; do
                [[ $chars[j] == [\"\'\\] ]] || word+=$chars[j]
                ((j++))
              done
              ((j--))
              if [[ -z $word ]]; then
                ok=0
                break
              fi
              pending+=("$strip:$word")
            fi
            ;;
        esac
      done
      lit=0
      [[ -n $q ]] && lit=1
      literal+=($lit)
    done
    if ((! ok)) || [[ -n $q ]] || (($#pending)); then
      REPLY=$1
      return
    fi
    local -i k
    local trimmed backslashes
    # Only space and tab, like Go; [[:blank:]] can match Unicode spaces.
    for ((k = 1; k <= $#lines; k++)); do
      ((literal[k])) && continue
      line=$lines[k]
      trimmed=${line%%[$blanks]#}
      backslashes=${(M)trimmed%%\\#}
      # Removing an escaped blank would join the next line onto this one.
      if (($#backslashes % 2 && $#trimmed < $#line)); then
        trimmed=${line[1,$#trimmed+1]}
      fi
      lines[k]=$trimmed
    done
    while (($#lines)) && [[ -z $lines[-1] ]]; do
      lines[-1]=()
    done
    REPLY="${(pj:\n:)lines}"$'\n'
  }

  # --- argument parsing ------------------------------------------------------

  parse_args() {
    local arg
    while (($#)); do
      arg=$1
      shift
      case $arg in
        -f | --fix) opt_fix=1 ;;
        -d | --diff) opt_diff=1 ;;
        -v | --verbose) opt_verbose=1 ;;
        --shellcheck) opt_shellcheck=1 ;;
        -h | --help) opt_help=1 ;;
        -V | --version) opt_version=1 ;;
        --exclude=*) excludes+=("${arg#*=}") ;;
        --stdin-filename=*) stdin_name=${arg#*=} ;;
        -x | --exclude | --stdin-filename)
          (($#)) || usage_error "option requires an argument: $arg"
          if [[ $arg == --stdin-filename ]]; then
            stdin_name=$1
          else
            excludes+=("$1")
          fi
          shift
          ;;
        --)
          operands+=("$@")
          break
          ;;
        -?*) usage_error "unknown option: $arg" ;;
        *) operands+=("$arg") ;;
      esac
    done

    local -i stdin=$((${operands[(Ie)-]} > 0))
    local pattern problem_text
    for pattern in $excludes; do
      problem_text=$(
        setopt local_options no_extended_glob
        [[ '' == ${~pattern} ]] 2>&1
      )
      [[ -n $problem_text ]] && usage_error "invalid --exclude pattern: $pattern"
    done
    if ((opt_fix && opt_diff)); then
      usage_error '--fix and --diff cannot be combined'
    elif ((stdin && $#operands > 1)); then
      usage_error '- cannot be combined with other paths'
    elif [[ -n $stdin_name ]] && ((! stdin)); then
      usage_error '--stdin-filename requires - as the path'
    fi
  }

  # --- discovery -------------------------------------------------------------

  first_line() {
    REPLY=''
    { IFS= read -r REPLY < "$1"; } 2> /dev/null
    REPLY=${REPLY[1,256]}
  }

  # Prints zsh, other or none for a first line.
  line_kind() {
    case $1 in
      '#compdef'* | '#autoload'*) REPLY=zsh ;;
      '#!'*)
        interpreter "${1#??}"
        [[ $REPLY == zsh ]] || REPLY=other
        ;;
      *) REPLY=none ;;
    esac
  }

  # The program name a shebang runs, looking past env, its options and its
  # variable assignments. Fields split on blanks and a carriage return, like Go.
  interpreter() {
    local -a fields=(${(s: :)${1//[$'\t\r']/ }})
    REPLY=''
    (($#fields)) || return 0
    REPLY=${fields[1]:t}
    [[ $REPLY == env ]] || return 0
    REPLY=''
    local field
    local -i skip=0
    for field in $fields[2,-1]; do
      if ((skip)); then
        skip=0
      elif [[ $field == -[uCP] ]]; then
        skip=1
      elif [[ $field != -* && $field != *=* ]]; then
        REPLY=${field:t}
        return 0
      fi
    done
  }

  is_zsh_candidate() { # path name
    local name=$2 zsh_name=0
    first_line "$1"
    line_kind "$REPLY"
    case $REPLY in
      zsh) return 0 ;;
      other) return 1 ;;
    esac
    [[ $name == *.zsh ]] && zsh_name=1
    if ((! zsh_name)) && [[ $name != .z* && $name == *.* ]]; then
      return 1
    fi
    ((zsh_name || ${+startup_names[$name]}))
  }

  # Exclude patterns are zsh globs without extended_glob; close to Go's
  # filepath.Match, though (a|b) alternation also works.
  excluded() { # name is_dir
    setopt local_options no_extended_glob
    local pattern
    if (($2)) && [[ $1 == .* || $1 == node_modules ]]; then
      return 0
    fi
    for pattern in $excludes; do
      [[ $1 == ${~pattern} ]] && return 0
    done
    return 1
  }

  # shfmt prints nothing for a file EditorConfig ignores.
  ignored() {
    local out
    out=$(print -r -- : | command shfmt --apply-ignore --filename "$1" 2> /dev/null)
    (($? == 0)) && [[ -z $out ]]
  }

  # Lexical cleanup like Go's filepath.Clean, so equal paths are checked once.
  clean_path() {
    local p=$1 seg
    local -i abs=0
    local -a out=()
    [[ $p == /* ]] && abs=1
    for seg in ${(s:/:)p}; do
      case $seg in
        '' | .) ;;
        ..)
          if (($#out)) && [[ $out[-1] != .. ]]; then
            out[-1]=()
          elif ((! abs)); then
            out+=(..)
          fi
          ;;
        *) out+=("$seg") ;;
      esac
    done
    REPLY=${(j:/:)out}
    if ((abs)); then
      REPLY=/$REPLY
    elif [[ -z $REPLY ]]; then
      REPLY=.
    fi
  }

  add_file() {
    clean_path "$1"
    local cleaned=$REPLY
    ((${+seen[$cleaned]})) && return
    seen[$cleaned]=1
    files+=("$cleaned")
  }

  # Joins like Go's filepath.Join for the paths a walk produces.
  join_path() {
    local dir=$1
    while [[ $dir == ./?* ]]; do
      dir=${dir#./}
    done
    case $dir in
      . | ./) REPLY=$2 ;;
      /) REPLY=/$2 ;;
      *) REPLY=${dir%/}/$2 ;;
    esac
  }

  # Depth-first walk in byte order, without following symlinks.
  walk() {
    local LC_COLLATE=C
    # Not "path": that name is tied to $PATH.
    local dir=$1 entry name file
    if [[ ! -r $dir || ! -x $dir ]]; then
      print -ru2 -- "zsh-lint: open $dir: permission denied"
      ((problems++))
      return
    fi
    local -a entries=("$dir"/*(DN))
    for entry in $entries; do
      name=${entry:t}
      join_path "$dir" "$name"
      file=$REPLY
      if [[ -d $entry && ! -L $entry ]]; then
        excluded "$name" 1 && continue
        walk "$file"
      elif [[ -f $entry && ! -L $entry ]]; then
        excluded "$name" 0 && continue
        is_zsh_candidate "$file" "$name" && ! ignored "$file" && add_file "$file"
      fi
    done
  }

  # --- checks ----------------------------------------------------------------

  zsh_check() { # content -> REPLY message, status
    REPLY=$(print -rn -- "$1" | command zsh -fn 2>&1)
    local -i rc=$?
    ((rc == 0)) && [[ -z $REPLY ]]
  }

  check_syntax() { # name content
    zsh_check "$2" && return 0
    if [[ -z $REPLY ]]; then
      problem "$1" 0 'syntax error'
      return 1
    fi
    local line
    for line in "${(@f)REPLY}"; do
      if [[ $line == zsh:* ]]; then
        report "$1:${line#zsh:}"
      else
        report "$1: $line"
      fi
    done
    return 1
  }

  check_style() { # name content
    local line
    local -i number=0
    split_lines "$2"
    for line in "$reply[@]"; do
      ((number++))
      [[ $line =~ $re_quoted_default ]] && problem "$1" $number "$msg_quoted_default"
      [[ $line =~ $re_spaced_alternation ]] && problem "$1" $number "$msg_spaced_alternation"
      [[ $line =~ $re_braced_array ]] && problem "$1" $number "$msg_braced_array"
    done
    return 0
  }

  run_shellcheck() { # name content
    local output finding
    output=$(print -rn -- "$2" | command shellcheck --shell=bash --format=gcc \
      --exclude=$shellcheck_excludes - 2>&1)
    local -i rc=$?
    if ((rc >= 126)); then
      problem "$1" 0 "could not run shellcheck: exit status $rc"
      return
    fi
    output=${output##[[:space:]]#}
    output=${output%%[[:space:]]#}
    [[ -z $output ]] && return
    local -a findings=("${(@f)output}")
    for finding in $findings; do
      if [[ $finding == *': error: '*'[SC1'* ]]; then
        ((unchecked++))
        ((opt_verbose)) && print -ru$out_fd -- "$1: not shellchecked: ShellCheck cannot parse it"
        return
      fi
    done
    for finding in $findings; do
      [[ $finding == -:* ]] && finding=$1:${finding#-:}
      report "$finding"
    done
  }

  # --- formatting ------------------------------------------------------------

  # shfmt without printer flags, so EditorConfig applies. shfmt only picks the
  # zsh dialect from a shebang it knows, so the content gets #!/bin/zsh in
  # place of its own shebang (#!/hint/zsh, ...) or in front of its first line,
  # and the original is put back afterwards. REPLY is the output or, on
  # failure, the error.
  shfmt_run() { # config_path content
    local input=$2 out shebang=''
    # Input line number minus original line number, for error messages.
    local -i line_shift=0
    if [[ $input == '#!'* ]]; then
      shebang=${input%%$nl*}
      input=${input#"$shebang"}
      input="#!/bin/zsh$input"
    else
      # shfmt drops leading blank lines, but would keep them after a shebang.
      input=${input##($nl)#}
      line_shift=$(($#2 - $#input - 1))
      if [[ -z $input ]]; then
        REPLY=$2
        return
      fi
      input=$'#!/bin/zsh\n'$input
    fi
    # The status rides after the output, which keeps trailing newlines.
    out=$(
      print -rn -- "$input" | command shfmt --filename "$1" 2>&1
      print -n "x$?"
    )
    local -i rc=${out##*x}
    out=${out%x*}
    if ((rc != 0)); then
      out=${out%%[[:space:]]#}
      out=${out#"$1":}
      if [[ $out =~ '^([0-9]+)(:.*)$' ]]; then
        out=$((match[1] + line_shift))$match[2]
      fi
      REPLY=$out
      return 1
    fi
    if [[ $out != $'#!/bin/zsh\n'* ]]; then
      REPLY='shfmt dropped the shebang'
      return 1
    fi
    out=${out#*$nl}
    [[ -n $shebang ]] && out=$shebang$nl$out
    REPLY=$out
  }

  # Subscript spans of $1 as "start end" pairs of the text between the
  # brackets, and the texts themselves, into the named arrays.
  subscripts() { # text spans_var texts_var
    local rest=$1
    local -i offset=0
    local -a spans texts
    while [[ $rest =~ $re_subscript ]]; do
      spans+=("$((offset + MBEGIN + 2)) $((offset + MEND - 1))")
      texts+=("${rest[MBEGIN+2,MEND-1]}")
      rest=${rest[MEND+1,-1]}
      ((offset += MEND))
    done
    set -A $2 "$spans[@]"
    set -A $3 "$texts[@]"
  }

  # restore_subscripts keeps every subscript as written in the source when
  # shfmt only changed its spacing, so [some-key] never becomes [some - key].
  restore_subscripts() { # src out -> REPLY
    local -a src_spans src_texts out_spans out_texts
    subscripts "$1" src_spans src_texts
    subscripts "$2" out_spans out_texts
    REPLY=$2
    (($#src_texts == $#out_texts)) || return 1

    local was now
    local -i i start end
    # Back to front, so earlier spans stay valid.
    for ((i = $#out_texts; i > 0; i--)); do
      was=$src_texts[i] now=$out_texts[i]
      [[ $was == "$now" || $was$now == *$nl* ]] && continue
      [[ ${was//[[:space:]]/} == "${now//[[:space:]]/}" ]] || continue
      start=${out_spans[i]% *} end=${out_spans[i]#* }
      REPLY=${REPLY[1,start-1]}$was${REPLY[end+1,-1]}
    done
  }

  format() { # name config_path content -> REPLY
    local name=$1 config=$2 src=$3 out restored

    if ! shfmt_run "$config" "$src"; then
      skip "$name" "the zsh parser cannot parse it: $name:$REPLY"
      REPLY=$src
      return
    fi
    out=$REPLY

    if ! restore_subscripts "$src" "$out"; then
      skip "$name" 'formatting would change subscripts'
      REPLY=$src
      return
    fi
    restored=$REPLY

    # Restored subscripts must format back to the same output, which shows
    # they only differ in spacing shfmt controls.
    if [[ $restored != "$out" ]] && { ! shfmt_run "$config" "$restored" || [[ $REPLY != "$out" ]]; }; then
      skip "$name" 'formatting would change subscripts'
      REPLY=$src
      return
    fi

    if [[ $restored != "$src" ]] && ! zsh_check "$restored"; then
      skip "$name" 'the formatted result is not valid zsh'
      REPLY=$src
      return
    fi

    REPLY=$restored
  }

  # --- per-file flow ---------------------------------------------------------

  report_changes() { # name src cleaned final
    ((opt_fix)) && return
    [[ $2 == "$4" ]] && return
    if ((opt_diff)); then
      print -ru$out_fd -- "diff $1 $1"
      # -L is the label flag in GNU, BSD and BusyBox diff.
      command diff -u -L "$1" -L "$1" =(print -rn -- "$2") =(print -rn -- "$4") >&$out_fd
    fi
    if [[ $3 == "$4" ]]; then
      problem "$1" 0 'trailing whitespace or a missing final newline'
    else
      problem "$1" 0 'not formatted; run zsh-lint --fix'
    fi
  }

  # lint checks one file's content; REPLY is the content --fix leaves.
  lint() { # name config_path src
    local name=$1 config=$2 src=$3 cleaned result checked
    local -i valid=0
    clean "$src"
    cleaned=$REPLY
    result=$cleaned

    if check_syntax "$name" "$src"; then
      valid=1
      format "$name" "$config" "$cleaned"
      result=$REPLY
    fi
    report_changes "$name" "$src" "$cleaned" "$result"
    if ((! valid)); then
      REPLY=$result
      return
    fi

    # Reports refer to the lines of the content that ends up on disk.
    checked=$src
    ((opt_fix)) && checked=$result
    check_style "$name" "$checked"
    ((opt_shellcheck)) && run_shellcheck "$name" "$checked"
    REPLY=$result
  }

  # Replaces the file that $1 resolves to with a renamed temporary file in the
  # same directory, so the file is never left half written. A file that is not
  # writable is refused. A file with other hard links or another owner, or one
  # whose directory takes no temporary file, is written in place.
  write_file() { # path content
    local target=${1:A} tmp mode
    local -A stat
    [[ -w $target ]] || return 1
    zstat -o -H stat -- "$target" 2> /dev/null || return 1
    mode=$stat[mode]
    if ((stat[nlink] != 1 || stat[uid] != EUID)) ||
      ! tmp=$(command mktemp "${target:h}/.${target:t}.XXXXXX" 2> /dev/null); then
      { print -rn -- "$2" > "$target"; } 2> /dev/null
      return
    fi
    if { print -rn -- "$2" > "$tmp"; } 2> /dev/null &&
      command chmod ${mode[-3,-1]} "$tmp" 2> /dev/null &&
      command mv -f "$tmp" "$target" 2> /dev/null; then
      return 0
    fi
    command rm -f "$tmp"
    return 1
  }

  run_paths() {
    local operand file src final summary
    (($#operands)) || operands=(.)

    for operand in $operands; do
      if [[ ! -e $operand ]]; then
        print -ru2 -- "zsh-lint: stat $operand: no such file or directory"
        ((problems++))
      elif [[ -d $operand ]]; then
        walk "$operand"
      elif [[ ! -f $operand ]]; then
        print -ru2 -- "$operand: not a regular file, ignored"
      else
        first_line "$operand"
        line_kind "$REPLY"
        if [[ $REPLY == other ]]; then
          print -ru2 -- "$operand: not a zsh file, ignored"
        else
          add_file "$operand"
        fi
      fi
    done

    for file in $files; do
      if [[ ! -r $file ]]; then
        problem "$file" 0 "open $file: permission denied"
        continue
      fi
      # Unset for an empty file.
      src=${mapfile[$file]-}
      lint "$file" "$file" "$src"
      final=$REPLY
      ((opt_fix)) || continue
      [[ $src == "$final" ]] && continue
      if ! write_file "$file" "$final"; then
        problem "$file" 0 'could not write the file'
        continue
      fi
      ((fixed++))
      print -ru$out_fd -- "$file: fixed"
    done

    summary="Checked $#files files: $fixed fixed, $skipped not formattable"
    ((opt_shellcheck)) && summary+=", $unchecked not shellchecked"
    print -ru$out_fd -- "$summary, $problems problem(s)."
    ((problems == 0))
  }

  run_stdin() {
    local src='' chunk='' name=$stdin_name config=$stdin_name
    # sysread keeps every byte, NUL included; it fails at end of input.
    while sysread chunk; do
      src+=$chunk
    done
    if [[ -z $name ]]; then
      name='<stdin>' config=stdin
    fi
    ((opt_fix)) && out_fd=2

    lint "$name" "$config" "$src"

    if ((opt_fix)); then
      print -rn -- "$REPLY"
      return 0
    fi
    ((problems == 0))
  }

  # --- main ------------------------------------------------------------------

  parse_args "$@"
  if ((opt_help)); then
    print -rn -- "$usage"
    return 0
  fi
  if ((opt_version)); then
    if (($+commands[shfmt])); then
      print -r -- "zsh-lint $version (zsh, shfmt $(command shfmt --version))"
    else
      print -r -- "zsh-lint $version (zsh)"
    fi
    return 0
  fi

  local tool
  local -a tools=(zsh shfmt)
  ((opt_shellcheck)) && tools+=(shellcheck)
  for tool in $tools; do
    if ((! $+commands[$tool])); then
      print -ru2 -- "zsh-lint: $tool is required"
      return 127
    fi
  done

  if ((${operands[(Ie)-]})); then
    run_stdin
  else
    run_paths
  fi
)

# Run only when executed, not when sourced. zsh reads a script as it runs,
# so exit before reading on: --fix may have rewritten this file.
if [[ $ZSH_EVAL_CONTEXT == toplevel ]]; then
  zsh-lint "$@"
  exit
fi
