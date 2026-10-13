# ADR-0021: Shell completion is cache-only and side-effect free; scripting uses `--json`

- **Status:** Accepted (amended 2026-09-25: status --json fails on a failed handshake; the --json gap is closed; amended 2026-10-13: one set of field names, flags in any order, exit status 2 for usage errors)
- **Date:** 2026-07-05 (completion 2026-07-11)
- **Sources:** cc3a8f2, 224efb2, 79f4ba1, 0c2224a, 0dadcbd; `cmd/soma/completion.go`, `internal/channels` `PeekChannelsFromCache`

## Context

Channel IDs could only be discovered in the TUI, so scripts and shell
completions had nothing to work with. A completion helper runs on every
Tab press, so it must be instant and must never change anything.

## Decision

- `soma completion channels` reads only the local catalog cache through a
  side-effect-free peek. It never spawns the daemon, never touches the
  network, and never moves a corrupt cache aside. No cache means no
  completions.
- Completion scripts are embedded from plain files so packages can also
  install them directly, and a drift-guard test keeps them in sync with
  the CLI surface. `completion` and `--version`/`--help` dispatch before
  the config is loaded so a broken config cannot break them.
- Machine-readable output is a convention: `status`, `list` and `favorite`
  take `--json`, and `status --json` never exits non-zero for a daemon
  that is not running or cannot be reached, so polling status bars keep
  parsing. A daemon that answers but fails the handshake (one speaking
  another protocol version, ADR-0006) is an error instead (amended
  2026-09-25): it may be playing, and reporting it as stopped was false.
- Global client flags given to `soma daemon` are refused by name rather
  than silently ignored.
- The JSON shapes share their field names (amended 2026-10-13): a
  channel's ID and title are always `channelId` and `channelTitle`, and a
  track's title `trackTitle`, as in the playback state. `list` used `id`
  and `title`, `favorite` `title`, and `history` `title` for the track,
  so `title` meant the channel in two commands and the track in a third.
  The CLI renames history's `title` itself rather than the wire's
  `protocol.HistoryEntry`, which an older daemon still running would
  otherwise answer without. Changed without a breaking-change commit: no
  one was scripting against the old names.
- A command's own flags may come before or after its arguments (amended
  2026-10-13): `soma play groovesalad --json` used to fail as a second
  channel, since the flag package stops at the first positional
  argument. `parseFlags` parses on past it. `volume` keeps its own
  parsing, since its argument may be a negative number.
- The exit status tells a script what went wrong (amended 2026-10-13): 0
  for success, 1 for a command that ran and failed, 2 for a command line
  soma cannot make sense of (an unknown flag, a wrong number of
  arguments, a bad value). Usage errors used to exit 1 and unknown flags
  2, as the flag package does; `failUsage` now exits 2 for all of them.

## Consequences

- Every new client command should accept `--json`. The gap this record
  once listed is closed (amended 2026-09-25): `play`, `next`, `prev`,
  `pause`, `stop` and `volume` print the resulting playback state
  (6d5beb6), and `history` and `lastfm status` print their own shapes.
  Only `lastfm login`, `lastfm logout` and `daemon stop` report nothing
  worth parsing and take no `--json`.
