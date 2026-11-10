# Prints what zsh's completion system offers at the end of each of the
# given command lines: a "line <n>" header per command line, in order,
# followed by one "match <candidate>" line per candidate.
#
#   zsh -f zsh-complete.zsh <directory holding _soma> <work dir> <line>...
#
# Completion functions only run inside the line editor, which wants a
# terminal, so for every line this starts an interactive zsh on a pty of its
# own (zpty), types the line and a Tab into it, and has that shell print the
# matches it is about to offer instead of showing a menu. The approach is
# that of Valodim's zsh-capture-completion.
#
# Exit status: 0 on success, 3 when this zsh cannot do it (no zpty module,
# or the inner shell did not come up).

zmodload zsh/zpty 2>/dev/null || exit 3

typeset fdir=$1 work=$2
shift 2

cat > $work/zsh-complete-init.zsh <<'EOF'
PROMPT=
fpath=($SOMA_COMPLETION_FPATH $fpath)
autoload -U compinit
compinit -u -d $SOMA_COMPLETION_WORK/zcompdump
bindkey '^I' complete-word
bindkey '^M' undefined
bindkey '^J' undefined
# A line holding a NUL is printed before and after the matches, to tell
# them from the echo of the typed line; the shell leaves after the second.
null-line() { echo -E - $'\0' }
compprefuncs=( null-line )
comppostfuncs=( null-line exit )
zstyle ':completion:*' list-grouped false
zstyle ':completion:*' insert-tab false
zstyle ':completion:*' list-separator ''
compadd() {
    # With -O, -A or -D the caller only wants an array filled.
    if [[ ${@[1,(i)(-|--)]} == *-(O|A|D)\ * ]]; then
        builtin compadd "$@"
        return $?
    fi
    # Let compadd do the matching into an array, and print the matches.
    typeset -a __hits
    builtin compadd -A __hits "$@"
    [[ -n $__hits ]] || return
    typeset __hit
    for __hit in $__hits; do
        echo -E - "match $__hit"
    done
}
echo soma-completion-ready
EOF

export SOMA_COMPLETION_FPATH=$fdir SOMA_COMPLETION_WORK=$work

# capture copies the "match" lines between the two NUL lines.
capture() {
    typeset -i between=0
    typeset out
    while IFS= read -r out; do
        if [[ $out == *$'\0'* ]]; then
            (( between++ )) && return 0
            continue
        fi
        if (( between )) && [[ $out == match\ * ]]; then
            echo -E - ${out%$'\r'}
        fi
    done
    return 0
}

typeset line reply
typeset -i n=0 up
for line in "$@"; do
    echo "line $(( n++ ))"
    zpty z zsh -f -i
    zpty -w z "source $work/zsh-complete-init.zsh"
    up=0
    repeat 100; do
        zpty -r z reply || break
        if [[ $reply == *soma-completion-ready* ]]; then
            up=1
            break
        fi
    done
    (( up )) || exit 3
    zpty -w -n z "$line"$'\t'
    while zpty -r z; do :; done | capture
    zpty -d z
done
