# ADR-0019: MPRIS and the system tray live in the daemon process

- **Status:** Accepted
- **Date:** 2026-02-05 (moved into the daemon 2026-07-05, tray 2026-07-07)
- **Sources:** d0a22e0, 95fb723, d1d33df, be4000e, a6f0d6f; `internal/platform`, `internal/server/mpris.go`

## Context

Media keys and a tray icon only make sense if they work while the TUI is
closed, which after ADR-0001 means they must belong to the daemon.

## Decision

- MPRIS over D-Bus on Linux, a no-op stub elsewhere, using the
  `_linux.go` / `_other.go` build-tag pair. Shared message types live in
  `platform/command.go` rather than the per-OS files.
- The tray lives in the daemon and reuses the MPRIS command router, so its
  clicks map onto the existing playback methods. It owns the native GUI
  run loop: the daemon serves connections on a goroutine and blocks on the
  tray on the main goroutine. `--no-tray` and a headless check (CGSession
  on macOS, display/bus environment on Linux) skip it so the daemon runs
  anywhere.
- Play-ish commands are dispatched off the D-Bus goroutine because they
  block on the network; Shutdown likewise, to avoid deadlocking the
  dispatcher. MPRIS `Quit` performs a real shutdown since `CanQuit` is
  advertised.
- Redundant tray Pause/Stop items were removed: `PlayPause` already tears
  down the stream when playing.

## Consequences

- macOS release builds compile with cgo per architecture before `lipo`.
- `mpris:artUrl` is not set yet; adding it requires threading the channel
  image through the server-to-platform boundary (TODO.md).
