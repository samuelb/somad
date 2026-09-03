---
title: Usage
description: Start the TUI, control playback from the command line, use keyboard shortcuts, and configure Somad.
weight: 20
---

Run `soma` with no arguments to open the terminal UI. It starts the playback
daemon in the background if one isn't running yet:

```sh
soma
```

Pick a channel with the arrow keys and press <kbd>Enter</kbd>. Quitting the
TUI with <kbd>q</kbd> leaves the music playing; run `soma` again to pick the
session back up, or `soma stop` to silence it.

## Commands

Every command works without the TUI, which makes them handy for scripts,
keybindings and status bars.

| Command | Description |
| ------- | ----------- |
| `soma` | Start the TUI (spawns the playback daemon if needed); `--shutdown-on-exit` stops playback and the daemon on quit |
| `soma play [channel]` | Play a channel by ID or name match, or resume the last played channel when omitted |
| `soma list [--json]` | List all channels (favorites first, marked with `*`) |
| `soma favorite [--json] <channel>` | Toggle a channel's favorite flag (`fav` works too) |
| `soma next` / `soma prev` | Play the next / previous channel (favorites first, wraps around) |
| `soma pause` | Toggle pause (live radio: unpausing rejoins the live stream) |
| `soma stop` | Stop playback |
| `soma status [--json]` | Show what is playing (`--json` for status bars and scripts) |
| `soma volume [<0-100>\|+n\|-n]` | Show the volume, set it, or adjust it relative to the current value |
| `soma daemon` | Run the playback daemon in the foreground (`--no-tray` hides the tray icon; `--listen`, `--tls`, `--psk-file` serve [remote frontends](#remote-control-over-tcp)) |
| `soma daemon stop` | Shut down the playback daemon |
| `soma completion <bash\|zsh>` | Print a completion script for the given shell |
| `soma --version` | Print version information |

Every client command also accepts the connection flags described under
[Remote control over TCP](#remote-control-over-tcp), given before the
command, to control a daemon running on another machine.

## Keyboard controls

| Key | Action |
| --- | ------ |
| <kbd>↑</kbd> / <kbd>k</kbd> | Navigate channels up |
| <kbd>↓</kbd> / <kbd>j</kbd> | Navigate channels down |
| <kbd>Enter</kbd> / <kbd>Space</kbd> | Play selected channel |
| <kbd>s</kbd> | Stop playback |
| <kbd>+</kbd> / <kbd>-</kbd> | Volume up / down |
| <kbd>f</kbd> / <kbd>*</kbd> | Toggle favorite |
| <kbd>/</kbd> | Filter channels |
| <kbd>q</kbd> / <kbd>Ctrl+C</kbd> | Quit the TUI (playback continues, unless started with `--shutdown-on-exit`) |

## Background playback

Audio is streamed and decoded by a separate `soma daemon` process that the
TUI and the CLI commands talk to over a Unix socket. It normally starts
automatically the first time it is needed, but you can also run it yourself
in the foreground with `soma daemon`, which is handy for watching its logs or
running it under a service manager.

By default the daemon keeps running until stopped explicitly with
`soma daemon stop` or the tray's *Quit* item. Set an idle timeout with
`soma daemon --idle-timeout` or the `server.idle_timeout` setting to make it
exit on its own once playback is stopped and no client is connected for that
long. If you'd rather have quitting the TUI take everything down, start it
with `soma --shutdown-on-exit` or set `tui.shutdown_on_exit: true` in the
[configuration file](#configuration).

While the daemon runs it shows a tray / menu-bar icon (macOS and Linux, where
a tray host is available) with the current track, a channel picker, playback
controls, a favorite toggle and a *Quit* item. Pass `soma daemon --no-tray`
or set `server.tray: false` to run without it. On a headless host the tray is
skipped automatically.

After an upgrade, the running daemon is restarted onto the new version the
next time you change channel, pause, or stop, never mid-song.

## Remote control over TCP

By default the daemon only listens on a local Unix socket. To control a
daemon on another machine, say a server wired to the living-room speakers,
make it additionally listen on TCP:

```sh
# on the machine with the speakers
soma daemon --listen 0.0.0.0:5454 --tls --psk-file ~/.config/somad/psk
```

`--tls` encrypts the connection; without your own certificate
(`--tls-cert`/`--tls-key`) a self-signed one is generated once and reused.
The daemon prints its SHA-256 fingerprint at startup, and
`soma daemon --show-cert` reprints it any time. `--psk-file` points at a file
holding a pre-shared key that TCP clients must know; it is verified with an
HMAC challenge-response and never travels over the wire.

On the laptop, point the frontend at the server, pin the certificate by its
fingerprint, and hand it the same key:

```sh
soma --server myserver:5454 --tls-fingerprint sha256:... --psk-file ~/somad-psk
```

That works with every command, or permanently via `$SOMAD_SERVER` and the
`client:` section of the [configuration file](#configuration). Instead of
pinning the fingerprint you can trust the certificate file itself
(`--tls-ca`) or, with a CA-issued certificate, use plain `--tls` with the
system trust store.

A listener reachable from other machines requires both TLS and a PSK; the
daemon refuses to start without them. On a trusted isolated network you can
pass `--insecure` (or set `server.insecure`) to run one open anyway.

With a remote server the client never auto-starts one, and a version-skewed
remote daemon is never restarted; mismatched builds keep working together as
long as they speak the same protocol version.

## Configuration

The daemon and TUI flags can also be set in a configuration file, which
matters because the daemon is usually auto-spawned and therefore runs without
any flags. It lives at:

- **Linux**: `$XDG_CONFIG_HOME/somad/config.yaml` (usually `~/.config/somad/config.yaml`)
- **macOS**: `~/Library/Application Support/somad/config.yaml`

On the first daemon start the file is created as a template with every
setting present but commented out. All settings are optional; anything
omitted keeps its built-in default, and explicit flags take precedence:

```yaml
server:
  # How long the daemon lingers with no connected clients and stopped
  # playback before exiting. "0" (the default) never exits on idle.
  idle_timeout: 5m

  # Show the system tray / menu-bar icon while the daemon runs. Default: true.
  tray: false

  # Also listen for remote frontends on TCP. Default: unset.
  listen: "0.0.0.0:5454"

  # Encrypt the TCP listener with TLS (auto-generated certificate unless
  # tls_cert/tls_key point at your own PEM pair).
  tls: true

  # Require TCP clients to present this pre-shared key; psk_file reads it
  # from a file instead. Set at most one of the two.
  psk: "change-me"

client:
  # Connect the TUI and CLI to a remote daemon instead of the local socket.
  server: "myserver:5454"

  # Trust the server's certificate by pinned fingerprint or by PEM file
  # (tls_ca). Either implies TLS; plain `tls: true` uses the system store.
  tls_fingerprint: "sha256:..."

  # Pre-shared key matching the server's psk; psk_file reads it from a file.
  psk: "change-me"

tui:
  # Stop playback and shut down the daemon when the TUI exits. Default: false.
  shutdown_on_exit: true
```

A config file that exists but fails to parse, or contains unknown keys,
stops the daemon from starting with an error naming the offending line. A
typo never silently falls back to defaults.

## Where Somad keeps its files

- **Config**: `~/.config/somad/` (Linux) or `~/Library/Application Support/somad/` (macOS)
- **State**: `~/.local/state/somad/` (Linux) or `~/Library/Application Support/somad/` (macOS).
  Also holds `server.log`, the log of the auto-spawned daemon, and the
  auto-generated TLS certificate.
- **Cache**: `~/.cache/somad/` (Linux) or `~/Library/Caches/somad/` (macOS)
- **Socket**: `$XDG_RUNTIME_DIR/somad.sock` (Linux) or a per-user temp
  directory (macOS); override with `$SOMAD_SOCKET`
