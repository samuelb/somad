# ADR-0003: The TUI holds no playback state of its own

- **Status:** Accepted
- **Date:** 2026-07-05
- **Sources:** 95fb723, f9445e6; `internal/app/model.go`, `commands.go`

## Context

Before the split, playback ran inline in the Bubble Tea update loop. A
playlist fetch or stream connect froze the whole UI for up to fifteen
seconds, and the model and the player could disagree about what was
playing.

## Decision

- The model renders from the latest server snapshot (ADR-0002) and sends
  commands over a `Backend` interface. It caches nothing about playback.
- All blocking work happens in the daemon. Play and stop are asynchronous
  from the UI's point of view; the last user action wins via generation
  counters (ADR-0014).
- Failures are always surfaced: failed requests show in the status bar,
  a lost connection shows an error screen, a gone channel tells the user.
  Nothing fails silently.

## Consequences

- The TUI is trivially testable with a fake backend, and `internal/app` is
  one of the best-covered packages.
- Any new playback feature needs a protocol method first, then a `Backend`
  method, then a key binding. The missing `PlayPause` in the TUI is an
  example of the last two steps being skipped (open item in TODO.md).
