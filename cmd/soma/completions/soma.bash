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
        daemon completion help version"

    # Flags whose value is the next word (or follows "=").
    case "$prev" in
    --tls-ca | --psk-file | --tls-cert | --tls-key)
        compopt -o default 2>/dev/null # complete filenames
        COMPREPLY=()
        return
        ;;
    --server | --tls-fingerprint | --listen | --idle-timeout)
        COMPREPLY=()
        return
        ;;
    --quality)
        COMPREPLY=($(compgen -W "highest high low" -- "$cur"))
        return
        ;;
    esac

    # Find the subcommand: the first non-flag word, skipping values of
    # value-taking global flags (given as the next word, after a split-out
    # "=", or inline in the same word).
    local i w cmd=""
    for ((i = 1; i < COMP_CWORD; i++)); do
        w="${COMP_WORDS[i]}"
        case "$w" in
        --server=* | --tls-ca=* | --tls-fingerprint=* | --psk-file=*) ;;
        --server | --tls-ca | --tls-fingerprint | --psk-file)
            ((i++))
            [[ "${COMP_WORDS[i]}" == "=" ]] && ((i++))
            ;;
        =) ;;
        -*) ;;
        *)
            cmd="$w"
            break
            ;;
        esac
    done

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
    list | status | next | prev | pause | stop)
        COMPREPLY=($(compgen -W "--json" -- "$cur"))
        ;;
    daemon)
        COMPREPLY=($(compgen -W "stop --idle-timeout --no-tray --quality --listen --tls
            --tls-cert --tls-key --psk-file --insecure --show-cert" -- "$cur"))
        ;;
    completion)
        COMPREPLY=($(compgen -W "bash zsh" -- "$cur"))
        ;;
    esac
}

complete -F _soma soma
