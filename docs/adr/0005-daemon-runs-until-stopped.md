# ADR-0005: The daemon runs until stopped explicitly

- **Status:** Accepted (supersedes the two-minute default from 95fb723)
- **Date:** 2026-07-07
- **Sources:** 3cd4a18; `internal/server/server.go` `DefaultIdleTimeout`

## Context

The first daemon exited after two idle minutes (no clients, playback
stopped). That "surprised users who expected the server to stay around
like any other daemon: reopening the TUI after a break paid the spawn
cost again."

## Decision

- `DefaultIdleTimeout` is 0: never exit on idle.
- `--idle-timeout` and `server.idle_timeout` restore the old behaviour.
  Idle means no registered client **and** playback stopped; the timer is
  armed at start so a daemon whose spawning client died is still reaped
  when a timeout is configured.
- Unauthenticated TCP connections do not count as clients and never keep
  the daemon alive (ADR-0007).

## Consequences

- `soma daemon stop` or the tray's Quit item is the normal way to end it.
- Documentation must describe the idle exit as opt-in.
