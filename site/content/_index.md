---
title: Somad · SomaFM in your terminal
description: Play and explore SomaFM radio from your terminal. Daemon-backed playback that outlives the TUI, plus a headless CLI. Linux and macOS.

# Copy for the hero, quick start, feature grid and "How it works" cards.
# The layout lives in templates/index.html; the body below is the Install
# and Usage prose.
extra:
  eyebrow: SomaFM radio client · Linux and macOS
  headline: SomaFM in your&nbsp;terminal.
  lede: >-
    Browse every SomaFM channel from a keyboard-driven TUI. Playback runs in a
    background daemon, so closing the terminal never stops the music, and a
    headless CLI controls it from anywhere.
  quickstart_text: >-
    One tap, one install, one command. Homebrew works on both macOS and Linux;
    packages for Debian, Fedora, Arch and Nix, plus plain binaries, are
    [below](#install).
  quickstart:
    - brew tap samuelb/tap
    - brew install somad
    - soma
  how_note: >-
    Frontends and daemon talk over a local socket, or over TCP with TLS and a
    pre-shared key when the speakers live on another machine.
    [Remote control details](#remote-control).

  features:
    - title: Music that outlives the TUI
      text: >-
        Playback runs in a background daemon. Quit the terminal UI and the music
        keeps playing; reopen it any time to pick the session back up.
    - title: Headless CLI
      text: >-
        `play`, `next`, `pause`, `stop`, `status`, `volume` and `list` work
        without the TUI, so they slot into scripts, keybindings and status bars.
    - title: Every SomaFM channel
      text: >-
        Browse and filter the full channel list, mark favorites, and jump
        straight back to your last-played channel on startup.
    - title: Live track info
      text: >-
        Artist and title arrive with the stream itself, so the TUI, the tray and
        media players always show what is playing.
    - title: Desktop notifications
      text: >-
        Opt in with `soma daemon --notify` for a notification on every track
        change, fired from the daemon so it works with the TUI closed.
    - title: Now-playing history
      text: >-
        Browse recent titles for a channel with `soma history` or the TUI's
        <kbd>h</kbd> overlay, backfilled from SomaFM when the daemon hasn't been
        running long.
    - title: Last.fm scrobbling
      text: >-
        Opt in with `soma lastfm login` for now-playing updates and scrobbles,
        fired from the daemon so it works with the TUI closed.
    - title: Remote control
      text: >-
        Run the daemon on the machine wired to the speakers and control it from
        your laptop over TCP, with TLS and a pre-shared key.
    - title: Tray icon and media keys
      text: >-
        A menu-bar or tray icon with channel picker and playback controls, and
        MPRIS on Linux so media keys keep working with the TUI closed.
    - title: Resilient streaming
      text: >-
        Buffered playback with a stall watchdog and automatic reconnection, so a
        flaky network means a pause, not a restart.
    - title: Fast to start
      text: >-
        The channel catalog is cached and refreshed in the background, so the
        TUI opens instantly.
    - title: Sleep timer
      text: >-
        `soma stop --in 45m` arms a sleep timer owned by the daemon, so it stops
        playback on its own even if you close the TUI or the terminal first.

  flow:
    - label: TUI & CLI
      text: >-
        `soma` opens the interface; `soma play`, `soma next`, `soma status` and
        friends work without it.
    - label: Daemon
      text: >-
        Starts on demand, streams and decodes the audio, tracks titles, shows
        the tray icon, and keeps playing after every client has gone.
    - label: SomaFM
      text: >-
        Plays each channel's best stream directly, AAC on macOS and MP3 on Linux
        (stream quality is configurable), and reconnects on its own when the
        network hiccups.
---

{% <prose id="install" title="Install"> %}
Somad runs on Linux and macOS. Every method installs a single binary called `soma`; no extra runtime or audio setup is needed. Packages and binaries come from the [latest release]({{ config.extra.repo }}/releases).

{% <cards> %}
{% <card title="Homebrew" sub="macOS and Linux"> %}
```sh
brew tap samuelb/tap
brew install somad
```

No compiler required. Upgrade later with `brew upgrade somad`. If Homebrew reports an "untrusted tap", run `brew trust samuelb/tap` and try again.
{% </card> %}

{% <card title="Debian / Ubuntu"> %}
Download the `.deb` package and install it:

```sh
sudo apt install ./somad_*_linux_$(dpkg --print-architecture).deb
```
{% </card> %}

{% <card title="Fedora / RHEL / openSUSE"> %}
Download the `.rpm` package and install it:

```sh
sudo dnf install ./somad-*.$(uname -m).rpm
```
{% </card> %}

{% <card title="Nix"> %}
Run it straight from the flake, or install it into your profile:

```sh
nix run github:samuelb/somad
nix profile install github:samuelb/somad
```
{% </card> %}

{% <card title="Arch Linux"> %}
Every release ships a pinned `PKGBUILD` plus the matching source tarball as release assets. Download the PKGBUILD and run:

```sh
makepkg -si
```
{% </card> %}

{% <card title="Pre-built binaries"> %}
- `soma-macos.dmg`: macOS, universal (Intel and Apple Silicon). Mount it and copy `soma` somewhere on your `PATH`.
- `soma_darwin_universal`: the same build as a bare binary.
- `soma_linux_amd64` and `soma_linux_arm64`: Linux.

Releases are signed and notarized when built with the project's Apple Developer ID. If macOS still refuses to run the binary (a source build, or an older unsigned release), remove the quarantine flag once (or use *Privacy &amp; Security › Open Anyway*). On Linux, make it executable:

<pre><code>xattr -d com.apple.quarantine ./soma   <span class="c"># macOS</span>
chmod +x ./soma                        <span class="c"># Linux</span></code></pre>
{% </card> %}

{% <card title="Build from source"> %}
Needs Go 1.25.13 or newer and no C libraries: on Linux, audio goes to PulseAudio or PipeWire directly, with the ALSA library loaded at runtime only as a fallback.

```sh
git clone https://github.com/samuelb/somad.git
cd somad
go build -o soma ./cmd/soma
```
{% </card> %}

{% <card title="Shell completions"> %}
The Debian, Arch and Nix packages install Bash and Zsh completions automatically. For a manual setup:

<pre><code><span class="c"># Bash</span>
soma completion bash | sudo tee /usr/share/bash-completion/completions/soma
<span class="c"># Zsh (then: rm ~/.zcompdump &amp;&amp; exec zsh)</span>
soma completion zsh | sudo tee /usr/local/share/zsh/site-functions/_soma</code></pre>

Completions cover every subcommand and flag, and `soma play` completes channel IDs from the local cache without starting the daemon or touching the network.
{% </card> %}

{% <card title="Background service" sub="systemd / launchd"> %}
The Debian and RPM packages install a systemd `--user` unit to `/usr/lib/systemd/user/soma.service`; enable it with:

```sh
systemctl --user daemon-reload
systemctl --user enable --now soma.service
```

Its `ExecStart` has a commented `--no-tray` variant for headless hosts. nfpm only builds Linux packages, so macOS gets nothing installed automatically — copy `packaging/launchd/com.samuelb.soma.plist` from the repo to `~/Library/LaunchAgents/`, edit the `soma` path inside it to match your install, and run:

```sh
launchctl load ~/Library/LaunchAgents/com.samuelb.soma.plist
```
{% </card> %}
{% </cards> %}
{% </prose> %}

{% <prose id="usage" title="Usage"> %}
Run `soma` with no arguments to open the terminal UI. It starts the playback daemon in the background if one isn't running yet. Pick a channel with the arrow keys and press <kbd>Enter</kbd>. Quitting with <kbd>q</kbd> leaves the music playing; run `soma` again to pick the session back up, or `soma stop` to silence it.

### Commands {#commands}

Every command works without the TUI, which makes them handy for scripts, keybindings and status bars.

| Command | Description |
|---|---|
| `soma` | Start the TUI (spawns the daemon if needed); `--shutdown-on-exit` stops playback and the daemon on quit |
| `soma play [--json] [channel]` | Play a channel by ID or name match, or resume the last played channel when omitted |
| `soma list [--json]` | List all channels (favorites first, marked with `*`) |
| `soma favorite [--json] <channel>` | Toggle a channel's favorite flag (`fav` works too) |
| `soma next [--json]` / `soma prev [--json]` | Play the next / previous channel (favorites first, wraps around) |
| `soma pause [--json]` | Toggle pause (live radio: unpausing rejoins the live stream) |
| `soma stop [--json]` | Stop playback |
| `soma stop --in <duration> [--json]` | Sleep timer: stop after this long (e.g. `45m`) instead of immediately; the daemon owns the timer, so it fires even after this command exits, and replaces any timer already pending |
| `soma stop --cancel [--json]` | Cancel a pending sleep timer without stopping now |
| `soma status [--json]` | Show what is playing |
| `soma volume [--json] [<0-100>\|+n\|-n]` | Show the volume, set it, or adjust it relative to the current value |
| `soma volume mute [--json]` | Toggle mute, restoring the previous volume |
| `soma history [--json] [-n N] [channel]` | Show recent now-playing titles, newest first (all channels, or one when given; `-n` bounds how many, default 20) |
| `soma lastfm login` | Authorize soma with your Last.fm account and save the session (see [Last.fm](#lastfm)) |
| `soma lastfm logout` | Remove the saved Last.fm session |
| `soma lastfm status [--json]` | Show whether Last.fm scrobbling is configured and logged in |
| `soma daemon` | Run the daemon in the foreground (`--no-tray` hides the tray icon; `--notify` shows a desktop notification on track change; `--listen`, `--tls`, `--psk-file` serve [remote frontends](#remote-control); `--gen-psk` generates a pre-shared key) |
| `soma daemon stop` | Shut down the daemon |
| `soma completion <bash\|zsh>` | Print a completion script for the given shell |
| `soma --version` | Print version information |

`--json` on `play`, `next`, `prev`, `pause`, `stop`, `status`, and `volume` prints the resulting playback state as a single JSON line instead of the human-readable message, for status bars and scripts.

{% <cols> %}
<div>

### Keyboard controls {#keys}

| Key | Action |
|---|---|
| <kbd>↑</kbd> / <kbd>k</kbd> | Navigate channels up (mouse wheel scrolling works too) |
| <kbd>↓</kbd> / <kbd>j</kbd> | Navigate channels down (mouse wheel scrolling works too) |
| <kbd>Enter</kbd> / <kbd>Space</kbd> | Play selected channel |
| <kbd>p</kbd> | Play / pause (pause stops the stream; play reconnects live) |
| <kbd>s</kbd> | Stop playback |
| <kbd>+</kbd> / <kbd>-</kbd> | Volume up / down (<kbd>=</kbd> / <kbd>_</kbd> work too) |
| <kbd>m</kbd> | Toggle mute (restores the previous level) |
| <kbd>f</kbd> / <kbd>*</kbd> | Toggle favorite |
| <kbd>F</kbd> | Toggle a favorites-only view (combines with the search filter) |
| <kbd>/</kbd> | Search channels: type to filter the list, Enter keeps the filter |
| <kbd>n</kbd> / <kbd>N</kbd> | Next / previous search match |
| <kbd>c</kbd> | Clear the search |
| <kbd>a</kbd> | About |
| <kbd>h</kbd> | Show recent now-playing history for the playing channel |
| <kbd>Esc</kbd> | Close the about screen / history overlay / cancel the search |
| <kbd>q</kbd> / <kbd>Ctrl+C</kbd> | Quit the TUI (playback continues, unless started with `--shutdown-on-exit`) |

</div>
<div>

### Background playback {#background-playback}

Audio is streamed and decoded by a separate `soma daemon` process that the TUI and the CLI talk to over a Unix socket. It starts automatically when first needed; run `soma daemon` yourself to watch its logs or run it under a service manager — see <a href="#install">Install</a> for the systemd unit and launchd plist this repo ships.

The daemon keeps running until `soma daemon stop` or the tray's *Quit* item. Set `--idle-timeout` (or `server.idle_timeout`) to make it exit on its own once playback is stopped and no client is connected. While it runs it shows a tray / menu-bar icon with the current track, a channel picker and playback controls; `--no-tray` or `server.tray: false` turns that off, and headless hosts skip it automatically.

After an upgrade, the running daemon is restarted onto the new version the next time you change channel, pause or stop, never mid-song.

`soma stop --in 45m` arms a sleep timer instead of stopping right away; the daemon owns it, so it fires even if you close the TUI or the terminal, and a new `--in` replaces it. `soma stop --cancel` drops a pending timer without stopping. `soma status` and the TUI status line show “sleep in Nm” while one is pending.

The server picks each channel's best stream quality by default; set `--quality` (or `server.quality`) to `highest`, `high`, or `low` to prefer a lower one, e.g. to save bandwidth — a channel lacking that exact quality falls back to the nearest one it has.

Set `--notify` (or `server.notify: true`) to show a desktop notification — title as the heading, artist and channel as the body — on every track change. Off by default, and fired from the daemon itself so it works with the TUI closed.

</div>
{% </cols> %}

### Last.fm {#lastfm}

{% <cols> %}
<div>

Somad can send now-playing updates and scrobbles to Last.fm, opt-in and driven from the daemon so it works with the TUI closed. First create an API key/secret pair at [last.fm/api/account/create](https://www.last.fm/api/account/create) and add them to the [configuration file](#configuration):

```yaml
lastfm:
  api_key: your-api-key
  api_secret: your-api-secret
```

Then authorize soma with your Last.fm account:

```sh
soma lastfm login
```

</div>
<div>

This prints an authorization URL (and tries to open it in a browser), waits for you to press Enter once you've approved it on last.fm, then saves the resulting session. `soma lastfm status` reports whether scrobbling is configured and logged in; `soma lastfm logout` removes the saved session. The session key lives in a separate file in the state directory, not the config file, so logging in never edits your hand-written config; a running daemon picks up a fresh login immediately, without a restart.

Once logged in, the daemon sends a now-playing update on every track change and scrobbles the previous track when it ends (next title change, stop, or channel switch) if it played for at least 30 seconds. Each track is scrobbled at most once: live radio cannot skip or rewind, so a pause, a stream drop and reconnect, or a switch to another channel and back while the same track is still on air resume the same play rather than start a new one, and only the time actually listened counts towards the 30 seconds. A title with no identifiable artist (many ambient/genre streams don't follow the “Artist - Title” convention) is never sent, since Last.fm scrobbles need one.

</div>
{% </cols> %}

### Remote control over TCP {#remote-control}

{% <cols> %}
<div>

By default the daemon only listens on a local Unix socket. To control a daemon on another machine, say a server wired to the living-room speakers, generate a pre-shared key and make it additionally listen on TCP:

<pre><code><span class="c"># on the machine with the speakers</span>
soma daemon --gen-psk
soma daemon --listen 0.0.0.0:5454 --tls --psk-file ~/.config/somad/psk</code></pre>

`soma daemon --gen-psk` writes 32 random bytes to the PSK file (`--psk-file`/`server.psk_file`, defaulting to a `psk` file next to the [configuration file](#configuration)) at mode `0600`, refusing to overwrite an existing one. `--tls` encrypts the connection; without your own certificate (`--tls-cert`/`--tls-key`) a self-signed one is generated once and reused. The daemon prints its SHA-256 fingerprint at startup, and `soma daemon --show-cert` reprints it. `--psk-file` points at that key, which TCP clients must know; it is verified with an HMAC challenge-response and never travels over the wire. Both the daemon and any client reject a PSK file that is readable by group or others, or owned by another user.

</div>
<div>

On the laptop, copy the key over, point the frontend at the server, and pin the certificate by its fingerprint:

```sh
scp myserver:~/.config/somad/psk ~/somad-psk
soma --server myserver:5454 \
     --tls-fingerprint sha256:... \
     --psk-file ~/somad-psk
```

That works with every command, or permanently via `$SOMAD_SERVER` and the `client:` section of the [configuration file](#configuration). Instead of pinning the fingerprint you can trust the certificate file itself (`--tls-ca`) or, with a CA-issued certificate, use plain `--tls` with the system trust store.

A listener reachable from other machines requires both TLS and a PSK; the daemon refuses to start without them (`--insecure` overrides that on a trusted isolated network). A remote daemon is never auto-started or restarted by the client.

</div>
{% </cols> %}

### Configuration {#configuration}

{% <cols> %}
<div>

Daemon and TUI flags can also be set in a configuration file, which matters because the daemon is usually auto-spawned and therefore runs without any flags. It lives at `~/.config/somad/config.yaml` on Linux and `~/Library/Application Support/somad/config.yaml` on macOS.

On the first daemon start the file is created as a template with every setting present but commented out. All settings are optional; anything omitted keeps its built-in default, and explicit flags take precedence. A file that fails to parse or contains unknown keys stops the daemon with an error naming the offending line, so a typo never silently falls back to defaults.

### Where Somad keeps its files {#files}

- **Config**: `~/.config/somad/` (Linux) or `~/Library/Application Support/somad/` (macOS)
- **State**: `~/.local/state/somad/` (Linux) or the same macOS directory. Also holds `server.log`, the generated TLS certificate, and `lastfm.json` (the Last.fm session from `soma lastfm login`, mode `0600`).
- **Cache**: `~/.cache/somad/` (Linux) or `~/Library/Caches/somad/` (macOS)
- **Socket**: `$XDG_RUNTIME_DIR/somad.sock` (Linux) or a per-user temp directory (macOS); override with `$SOMAD_SOCKET`

</div>

<pre class="config"><code>server:
  <span class="c"># Exit after this long with no clients and stopped playback.
  # "0" (the default) never exits on idle.</span>
  idle_timeout: 5m
  <span class="c"># Show the tray / menu-bar icon. Default: true.</span>
  tray: false
  <span class="c"># Preferred stream quality: highest (default), high, or low.</span>
  quality: high
  <span class="c"># Desktop notification on track change. Default: false.</span>
  notify: true
  <span class="c"># Also serve remote frontends over TCP ...</span>
  listen: "0.0.0.0:5454"
  <span class="c"># ... encrypted (auto-generated certificate unless
  # tls_cert/tls_key point at your own PEM pair) ...</span>
  tls: true
  <span class="c"># ... and authenticated (soma daemon --gen-psk writes this file).</span>
  psk_file: ~/.config/somad/psk

client:
  <span class="c"># Connect the TUI and CLI to a remote daemon.</span>
  server: "myserver:5454"
  <span class="c"># Pin the server certificate, or trust a PEM via tls_ca.</span>
  tls_fingerprint: "sha256:..."
  psk_file: ~/somad-psk

tui:
  <span class="c"># Stop playback and the daemon when the TUI exits.</span>
  shutdown_on_exit: true

lastfm:
  <span class="c"># Now-playing updates and scrobbling, off unless both are set.
  # Create a pair at last.fm/api/account/create.</span>
  api_key: your-api-key
  api_secret: your-api-secret
  <span class="c"># Normally left unset: "soma lastfm login" saves this
  # separately instead of editing this file.</span>
  session_key: your-session-key</code></pre>
{% </cols> %}
{% </prose> %}
