# SomaTUI

[![CI](https://github.com/samuelb/somatui/actions/workflows/ci.yml/badge.svg)](https://github.com/samuelb/somatui/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.25-blue.svg)](https://golang.org/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**✨ This project was entirely vibe-coded. ✨**

A modern, TUI (Terminal User Interface) client for streaming and exploring SomaFM radio channels. Built for Linux and macOS — other platforms are not supported and may not work.

![SomaTUI Demo](demo.gif)

## Features

- Client-server architecture: playback runs in a background server, so you
  can close the TUI and the music keeps playing
- The server starts automatically when needed and exits on its own once
  playback is stopped and no client is connected
- After an upgrade, the next command restarts the background server onto the
  new version automatically, resuming whatever was playing
- Headless CLI commands (`play`, `list`, `stop`, `status`, `volume`) for scripting
  and keybindings without opening the TUI
- Mark channels as favorites for quick access
- Browse and filter the full list of SomaFM radio channels
- Play high-quality MP3 streams directly in your terminal
- View real-time track information (artist/title) from ICY metadata
- Buffered streaming with automatic reconnection on network issues
- Styled UI with color-coded playback states and visual indicators
- Select and remember your last-played channel
- Fast startup with cached channels and background refresh
- Smooth, keyboard-driven navigation and playback controls
- MPRIS desktop integration (Linux) — media keys keep working even with the
  TUI closed

## Installation

### Homebrew (macOS and Linux)

```sh
brew tap samuelb/somatui
brew install somatui
```

This installs a pre-built binary from the [latest release](https://github.com/samuelb/somatui/releases),
so no compiler is required. Upgrade later with `brew upgrade somatui`.

Recent versions of Homebrew ask you to explicitly trust a third-party tap before
installing from it. If you see an "untrusted tap" error, run
`brew trust samuelb/somatui` and try again.

### Pre-built Binaries

1.  Download the latest release for your platform from the [Releases page](https://github.com/samuelb/somatui/releases).
2.  Extract the archive.
3.  Run the `somatui` executable.

#### macOS

After downloading, you may need to grant permission to run the application since it is not signed.

To do this, open a terminal and run:

```sh
xattr -d com.apple.quarantine /path/to/somatui
```

Alternatively, you can go to `System Preferences > Security & Privacy > General` and click `Open Anyway`.

#### Linux

After downloading, make the binary executable:

```sh
chmod +x /path/to/somatui
```

Then, you can run it from your terminal.

### Build from Source

Prerequisites: Go 1.25 or newer

On Linux, the ALSA development library is required for audio support:

```sh
# Debian/Ubuntu
sudo apt-get install libasound2-dev

# Fedora
sudo dnf install alsa-lib-devel

# Arch
sudo pacman -S alsa-lib
```

Then build:

```sh
git clone https://github.com/samuelb/somatui.git
cd somatui
go build -o somatui
```

## Usage

Simply run:

```sh
./somatui
```

This opens the TUI and automatically starts the playback server in the
background if one isn't running yet.

### Commands

| Command                    | Description                                              |
| -------------------------- | -------------------------------------------------------- |
| `somatui`                  | Start the TUI (spawns the playback server if needed)     |
| `somatui play [channel]`   | Play a channel by ID or name match, or resume the last played channel when omitted |
| `somatui list [--json]`    | List all channels (favorites first, marked with `*`)     |
| `somatui favorite [--json] <channel>` | Toggle a channel's favorite flag (`fav` works too) |
| `somatui next` / `somatui prev` | Play the next / previous channel (favorites first, wraps around) |
| `somatui pause`            | Toggle pause (live radio: unpausing rejoins the live stream) |
| `somatui stop`             | Stop playback                                            |
| `somatui status [--json]`  | Show what is playing (`--json` for status bars/scripts)  |
| `somatui volume [<0-100>\|+n\|-n]` | Show the volume, set it, or adjust it relative to the current value |
| `somatui server`           | Run the playback server in the foreground                |
| `somatui server stop`      | Shut down the playback server                            |
| `somatui --version`        | Print version information                                |

### Background playback

Audio is streamed and decoded by a separate `somatui server` process that the
TUI (and the CLI commands) talk to over a Unix socket. Quitting the TUI with
<kbd>q</kbd> leaves the music playing — reopen `somatui` any time to pick the
session back up, or use `somatui stop` to silence it. Once playback is
stopped and no client is connected, the server exits on its own after a grace
period (2 minutes by default, tunable with `somatui server --idle-timeout`).

### Keyboard Controls

| Key                                 | Action                          |
| ----------------------------------- | ------------------------------- |
| <kbd>↑</kbd> / <kbd>k</kbd>         | Navigate channels up            |
| <kbd>↓</kbd> / <kbd>j</kbd>         | Navigate channels down          |
| <kbd>Enter</kbd> / <kbd>Space</kbd> | Play selected channel           |
| <kbd>s</kbd>                        | Stop playback                   |
| <kbd>+</kbd> / <kbd>-</kbd>         | Volume up / down                |
| <kbd>f</kbd> / <kbd>*</kbd>         | Toggle favorite                 |
| <kbd>/</kbd>                        | Filter channels                 |
| <kbd>q</kbd> / <kbd>Ctrl+C</kbd>    | Quit the TUI (playback continues) |

## Data Storage

- **State**: `~/.local/state/somatui/` (Linux) or `~/Library/Application Support/somatui/` (macOS) —
  also holds `server.log`, the log of the auto-spawned playback server
- **Cache**: `~/.cache/somatui/` (Linux) or `~/Library/Caches/somatui/` (macOS)
- **Socket**: `$XDG_RUNTIME_DIR/somatui.sock` (Linux) or a per-user temp
  directory (macOS); override with `$SOMATUI_SOCKET`

## Dependencies

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) (TUI framework)
- [Bubbles](https://github.com/charmbracelet/bubbles) (TUI components)
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) (styling)
- [ansi](https://github.com/charmbracelet/x/ansi) (ANSI parsing)
- [oto/v3](https://github.com/ebitengine/oto) (audio output)
- [go-mp3](https://github.com/hajimehoshi/go-mp3) (MP3 decoding)

See `go.mod` for the full dependency list.

## Contributing

Contributions are welcome! Feel free to open issues or pull requests.

1. Fork the repo
2. Create a feature branch
3. Install git hooks: `lefthook install`
4. Make your changes
5. Submit a pull request

### Git Hooks

This project uses [lefthook](https://github.com/evilmartians/lefthook) to run linting and tests automatically before commits and pushes.

After cloning, install the hooks:

```sh
lefthook install
```

This sets up:

- **pre-commit**: runs `golangci-lint` and `go test -race` when `.go` files are staged
- **pre-push**: runs `golangci-lint` and `go test -race` on every push

If you need to bypass hooks for a work-in-progress commit, use `--no-verify`:

```sh
git commit --no-verify -m "WIP"
```

To install lefthook itself, see [installation instructions](https://github.com/evilmartians/lefthook/blob/master/docs/install.md) or run:

```sh
go install github.com/evilmartians/lefthook@latest
```

## License

[MIT](LICENSE)

---

_This project is not affiliated with SomaFM. All content and station streams are provided by [somafm.com](https://somafm.com/)._
