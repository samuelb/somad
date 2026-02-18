# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Project Overview

SomaTUI is a Bubble Tea TUI client for streaming SomaFM internet radio. It targets Linux and macOS only.

## Build & Development Commands

```sh
# Build (requires libasound2-dev on Linux)
go build -o somatui

# Run tests
go test -race ./...

# Run a single test
go test -race -run TestFunctionName ./...

# Run tests with coverage
go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

# Lint
golangci-lint run ./...

# Run benchmarks
go test -bench=. -benchmem ./...
```

Lefthook git hooks are configured — run `lefthook install` after cloning. Pre-commit runs lint+test on staged `.go` files; pre-push always runs both.

## Architecture

Single `main` package using the Bubble Tea (Elm-inspired) architecture: `Init() → Update(msg) → View()`.

**Central orchestrator**: `model.go` — holds all application state and coordinates components. The `model.Update()` method is the main state machine handling keyboard input, async messages, and MPRIS D-Bus commands.

**Playback flow**: User selects channel → `playChannel()` extracts MP3 playlist URL → fetches stream URL from `.pls` file → `AudioPlayer.Play(url)` starts HTTP streaming through a pipe into an MP3 decoder → `MetadataReader` polls the stream for ICY metadata every 10s → track updates flow back as `trackUpdateMsg`.

**Key interfaces**:
- `AudioPlayer` (player.go) — abstracts audio playback, enabling mock-based testing
- `MPRISCmdSender` (mpris_linux.go) — abstracts `tea.Program.Send` for D-Bus → model communication

**Platform-specific code**: `mpris_linux.go` (full D-Bus MPRIS2 implementation) and `mpris_other.go` (no-op stub). Build tags control which is compiled.

**Async pattern**: Commands in `commands.go` return `tea.Msg` values that feed back into `model.Update()`. Channel data loads from cache first (`fromCache: true`), then refreshes from network in the background every 10 minutes.

**State persistence**: `state.go` saves `LastSelectedChannelID` to XDG-compliant paths. Restored on startup to select the previously-played channel.

**Security**: `internal/security` package provides input validation:
- `ValidateURL()` — Ensures URLs are from somafm.com domains (SSRF protection)
- `ValidatePathNoTraversal()` — Rejects paths with `..` sequences
- `AllowTestHosts(t)` — Test helper to allow localhost/127.0.0.1 for httptest

## Testing Conventions

- Use `testify/assert` and `testify/require` for assertions
- Table-driven tests with `t.Run()` subtests for parameterized cases
- Test helpers: `newTestModel()`, `testChannels()`, `setStateDir(t)`, `setCacheDir(t)`
- Mock network calls with `httptest.NewServer`; override `somafmChannelsURL` var for channel fetch tests
- Use `t.TempDir()` + `t.Setenv("XDG_STATE_HOME", ...)` for filesystem tests
- `mockPlayer` implements `AudioPlayer` for playback flow tests without audio hardware
- MPRIS and Player internals (requiring D-Bus/audio hardware) are not unit tested

## Code Style

- golangci-lint configuration in `.golangci.yml` with security-focused linters (gosec, etc.)
- Discard error returns with `_ =` when intentional (e.g., `_ = resp.Body.Close()`)
- Lipgloss styles defined centrally in `styles.go`
