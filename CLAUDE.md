# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Soma is a client for SomaFM internet radio (Linux and macOS only), written in Go with a client-server architecture: playback runs in a background daemon so music keeps playing after the TUI exits. The project (Go module and package name) is `somad`; the compiled binary/command is `soma`.

## Commands

```sh
make build              # build ./soma (embeds version via ldflags)
make test               # go test -race ./...
make lint               # golangci-lint run ./... (config in .golangci.yml)
make check              # lint + test + vet
go test -race ./internal/server/ -run TestName   # run a single test
```

- Dependencies are vendored (`vendor/`); after changing `go.mod`, run `go mod tidy && go mod vendor`.
- On Linux, building needs `libasound2-dev` (ALSA headers); macOS needs nothing extra.
- Git hooks via lefthook run `golangci-lint` and `go test -race` on pre-commit and pre-push.
- CI enforces a minimum total test coverage of 60% (Linux job).

## Architecture

Two processes, one binary. `cmd/soma/main.go` dispatches subcommands: no args opens the TUI, `server` runs the playback daemon in the foreground, and `play`/`stop`/`status`/etc. are headless CLI clients. The TUI and CLI never touch audio directly — everything goes through the daemon.

**Wire protocol** (`internal/protocol`): newline-delimited JSON over a Unix domain socket (`SocketPath()` in `socket.go`; overridable with `$SOMAD_SOCKET`). Clients send `Request`s, the server replies with ID-correlated `Response`s and pushes `Event`s carrying full state snapshots. `protocol.Version` must match exactly between client and server; bump it on any incompatible wire change.

**Server** (`internal/server`): owns audio playback, the channel catalog, persisted state, MPRIS, and the tray icon. `spawnlock.go` ensures only one daemon runs. The server exits on its own after an idle timeout when playback is stopped and no client is connected.

**Client** (`internal/client`): protocol client shared by TUI and CLI. `spawn.go` auto-spawns the server when none is running and handles version-skew upgrades: a server whose version differs from the client's is restarted onto the new binary, but only at a moment that already interrupts playback (channel change, pause, stop) — never mid-song. Tests shrink `restartWait` to keep this fast.

**TUI** (`internal/app` + `internal/ui`): standard Bubble Tea Elm architecture (`model.go`, `update.go`, `view.go`, `commands.go`). The model holds no playback state of its own — it renders from the latest server snapshot and sends commands over its `Backend` interface. `internal/ui` has the list delegate and lipgloss styles.

**Supporting packages**:
- `internal/audio` — MP3 streaming/decoding (oto + go-mp3), ICY metadata, buffering/reconnection
- `internal/channels` — SomaFM channel catalog fetch/cache and channel selection by ID/name
- `internal/state` — persisted user state (favorites, last channel, volume) in XDG/macOS dirs
- `internal/config` — optional YAML config file; unknown keys or parse errors are fatal by design (no silent fallback to defaults)
- `internal/security` — all outbound HTTP must go through `security.NewRequest`/`ValidateURL`, which allowlists SomaFM hosts and re-validates redirects; tests add hosts via `securitytest`
- `internal/platform` — OS integration with build-tagged files (`mpris_linux.go` / `mpris_other.go`, `tray/`)
- `internal/atomicfile` — atomic file writes (temp file + rename), used by state/cache persistence
- `pkg/playlist` — PLS playlist parsing

**Platform-conditional code** uses Go build tags with `_linux.go` / `_other.go` file pairs; keep both sides in sync when changing such interfaces.
