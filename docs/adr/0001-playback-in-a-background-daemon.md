# ADR-0001: Playback runs in a background daemon, shipped in one binary

- **Status:** Accepted
- **Date:** 2026-07-05
- **Sources:** 95fb723, c82d8fc; `cmd/soma/main.go`, `internal/server`, `internal/client`

## Context

The TUI was the player. Quitting it killed the music and the MPRIS media
keys, and every restart paid the connect cost again. Users of a radio
client expect the music to keep playing after the window closes.

## Decision

- Split the program into a background playback server and thin clients.
  `internal/server` owns audio, stream resolution, reconnection, track
  titles, volume, the channel catalog, persisted state, MPRIS and the tray.
- Ship both in one binary: `soma` with no arguments opens the TUI,
  `soma daemon` runs the server, `play`/`stop`/`status`/… are headless CLI
  clients. The TUI and CLI never touch audio; everything goes through the
  daemon.
- Clients auto-spawn the daemon when none is running (see ADR-0004).
- The project was renamed from `somatui` to `somad` (binary `soma`) because
  it "has grown beyond a pure terminal client"; runtime identifiers changed
  with no migration of old data.

## Consequences

- Quitting the TUI with `q` leaves the music playing. `--shutdown-on-exit`
  or `tui.shutdown_on_exit` opts back into the old behaviour.
- A wire protocol became necessary (ADR-0002), and with it the questions of
  local authorization (ADR-0007) and version skew between client and
  daemon (ADR-0006).
- A headless CLI surface exists for scripting (ADR-0021).
