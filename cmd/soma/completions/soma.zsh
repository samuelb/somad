#compdef soma
# zsh completion for soma
#
# Install by copying this file, named _soma, into a directory on $fpath:
#   soma completion zsh > /usr/local/share/zsh/site-functions/_soma
#
# Channel arguments complete from the locally cached catalog only (via
# `soma completion channels`), so completing never starts the daemon or
# touches the network.

_soma_channels() {
    local -a chans
    local id title
    while IFS=$'\t' read -r id title; do
        chans+=("${id}:${title}")
    done < <(soma completion channels 2>/dev/null)
    _describe -t channels 'channel' chans
}

_soma() {
    local curcontext="$curcontext" state line ret=1
    typeset -A opt_args

    _arguments -C \
        '--server[connect to the soma daemon at this host:port instead of the local one]:host\:port:' \
        '--tls[use TLS for the --server connection]' \
        '--tls-ca[PEM certificate/CA file to trust (implies --tls)]:file:_files' \
        '--tls-fingerprint[pin the server certificate by SHA-256 fingerprint (implies --tls)]:fingerprint:' \
        '--psk-file[file holding the server'\''s pre-shared key]:file:_files' \
        '--shutdown-on-exit[stop playback and shut down the server when the TUI exits]' \
        '(- *)--version[print version information]' \
        '(- *)--help[show help]' \
        '1:command:->command' \
        '*::arg:->args' && ret=0

    case $state in
    command)
        local -a commands=(
            'play:play a channel by ID or name, or resume the last played one'
            'list:list all channels (favorites first)'
            'favorite:toggle a channel'\''s favorite flag'
            'next:play the next channel'
            'prev:play the previous channel'
            'pause:toggle pause'
            'stop:stop playback'
            'status:show what is playing'
            'volume:show, set, or adjust the playback volume'
            'daemon:run the playback server in the foreground'
            'completion:print a shell completion script'
            'help:show help'
            'version:print version information'
        )
        _describe -t commands 'soma command' commands && ret=0
        ;;
    args)
        case $words[1] in
        play)
            _arguments \
                '--json[print machine-readable JSON]' \
                '1:channel:_soma_channels' && ret=0
            ;;
        favorite | fav)
            _arguments \
                '--json[print machine-readable JSON]' \
                '1:channel:_soma_channels' && ret=0
            ;;
        list | status)
            _arguments '--json[print machine-readable JSON]' && ret=0
            ;;
        next | prev | pause | stop)
            _arguments '--json[print machine-readable JSON]' && ret=0
            ;;
        volume)
            _arguments \
                '--json[print machine-readable JSON]' \
                '1:volume (0-100, +n, -n, mute):(mute)' && ret=0
            ;;
        daemon)
            _arguments \
                '--idle-timeout[exit after this long with no clients and stopped playback (0 disables)]:duration:' \
                '--no-tray[do not show the system tray / menu-bar icon]' \
                '--quality[preferred stream quality]:quality:(highest high low)' \
                '--listen[also listen for frontends on this TCP host:port]:host\:port:' \
                '--tls[serve the TCP listener over TLS]' \
                '--tls-cert[PEM certificate for the TCP listener (implies --tls)]:file:_files' \
                '--tls-key[PEM private key belonging to --tls-cert]:file:_files' \
                '--psk-file[file holding the pre-shared key TCP clients must authenticate with]:file:_files' \
                '--gen-psk[generate a random pre-shared key at --psk-file, then exit]' \
                '--insecure[serve a non-loopback --listen address even without TLS and a PSK]' \
                '--show-cert[print the TLS certificate path and fingerprint, then exit]' \
                '1:action:(stop)' && ret=0
            ;;
        completion)
            _arguments '1:shell:(bash zsh)' && ret=0
            ;;
        esac
        ;;
    esac
    return ret
}

_soma "$@"
