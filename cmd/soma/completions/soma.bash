# bash completion for soma                                -*- shell-script -*-
#
# Install by sourcing this file from ~/.bashrc, e.g.:
#   source <(soma completion bash)
# or copy it into the bash-completion completions directory:
#   soma completion bash > /usr/share/bash-completion/completions/soma
#
# Channel arguments complete from the locally cached catalog only (via
# `soma completion channels`), so completing never starts the daemon or
# touches the network.
#
# It has to run on bash 3.2 (macOS) and without the bash-completion package,
# hence COMPREPLY=($(compgen ...)) where shellcheck would rather see mapfile.
# shellcheck shell=bash disable=SC2207

_soma() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    # COMP_WORDBREAKS splits "--flag=value" into "--flag", "=", "value";
    # rejoin so the flag handling below sees the flag as prev.
    if [[ "$cur" == "=" && "$prev" == -* ]]; then
        cur=""
    elif [[ "$prev" == "=" && $COMP_CWORD -ge 2 ]]; then
        prev="${COMP_WORDS[COMP_CWORD-2]}"
    fi

    local global_flags="--server --tls --tls-ca --tls-fingerprint --psk-file
        --shutdown-on-exit --version --help"
    local commands="play list favorite next prev pause stop status volume
        history lastfm daemon completion help version"

    # Flags whose value is the next word (or follows "=").
    case "$prev" in
    --tls-ca | --psk-file | --tls-cert | --tls-key)
        compopt -o default 2>/dev/null # complete filenames
        COMPREPLY=()
        return
        ;;
    --server | --tls-fingerprint | --listen | --idle-timeout | --in | -n)
        COMPREPLY=()
        return
        ;;
    --quality)
        COMPREPLY=($(compgen -W "highest high low" -- "$cur"))
        return
        ;;
    esac

    # bash 4 and later break the line at ":" as well as "=", so
    # "--server=host:5454" arrives as "--server" "=" "host" ":" "5454", and
    # the address would pass for the subcommand. Put such runs back
    # together: words holds the line up to the cursor as it was typed, and
    # cword is the index of its last word. bash 3.2 does not break words
    # there, and its words pass through as they are.
    local -a words
    local cword=-1 glue=0 i w
    for ((i = 0; i <= COMP_CWORD; i++)); do
        w="${COMP_WORDS[i]}"
        if ((cword > 0)) && [[ -n "$w" && "$w" != *[!=:]* ]]; then
            words[cword]+="$w"
            glue=1
        elif ((glue)); then
            words[cword]+="$w"
            glue=0
        else
            cword=$((cword + 1))
            words[cword]="$w"
        fi
    done

    # Find the subcommand: the first non-flag word, skipping the value of a
    # value-taking global flag given as the next word ("--flag=value" is a
    # single word here).
    local cmd=""
    for ((i = 1; i < cword; i++)); do
        w="${words[i]}"
        case "$w" in
        --server | --tls-ca | --tls-fingerprint | --psk-file) ((i++)) ;;
        -*) ;;
        *)
            cmd="$w"
            break
            ;;
        esac
    done

    # The cursor is inside such a value ("--server host:54"): nothing here
    # can complete it.
    if ((i > cword)); then
        COMPREPLY=()
        return
    fi

    if [[ -z "$cmd" ]]; then
        if [[ "$cur" == -* ]]; then
            COMPREPLY=($(compgen -W "$global_flags" -- "$cur"))
        else
            COMPREPLY=($(compgen -W "$commands" -- "$cur"))
        fi
        return
    fi

    case "$cmd" in
    play)
        if [[ "$cur" == -* ]]; then
            COMPREPLY=($(compgen -W "--json" -- "$cur"))
        else
            COMPREPLY=($(compgen -W "$(soma completion channels 2>/dev/null | cut -f1)" -- "$cur"))
        fi
        ;;
    favorite | fav)
        if [[ "$cur" == -* ]]; then
            COMPREPLY=($(compgen -W "--json" -- "$cur"))
        else
            COMPREPLY=($(compgen -W "$(soma completion channels 2>/dev/null | cut -f1)" -- "$cur"))
        fi
        ;;
    volume)
        COMPREPLY=($(compgen -W "--json mute" -- "$cur"))
        ;;
    history)
        if [[ "$cur" == -* ]]; then
            COMPREPLY=($(compgen -W "--json -n" -- "$cur"))
        else
            COMPREPLY=($(compgen -W "$(soma completion channels 2>/dev/null | cut -f1)" -- "$cur"))
        fi
        ;;
    list | status | next | prev | previous | pause)
        COMPREPLY=($(compgen -W "--json" -- "$cur"))
        ;;
    stop)
        COMPREPLY=($(compgen -W "--json --in --cancel" -- "$cur"))
        ;;
    daemon)
        COMPREPLY=($(compgen -W "stop --idle-timeout --no-tray --notify --quality --listen --tls
            --tls-cert --tls-key --psk-file --gen-psk --insecure --supervised --show-cert" -- "$cur"))
        ;;
    lastfm)
        # The action follows "lastfm" (at index i); only status takes a flag.
        if ((i + 1 < cword)); then
            if [[ "${words[i + 1]}" == status ]]; then
                COMPREPLY=($(compgen -W "--json" -- "$cur"))
            fi
        else
            COMPREPLY=($(compgen -W "login logout status" -- "$cur"))
        fi
        ;;
    completion)
        COMPREPLY=($(compgen -W "bash zsh" -- "$cur"))
        ;;
    esac
}

complete -F _soma soma
