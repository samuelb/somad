# ADR-0021: Shell completion is cache-only and side-effect free; scripting uses `--json`

- **Status:** Accepted (amended 2026-09-25: status --json fails on a failed handshake; the --json gap is closed; amended 2026-10-13: one set of field names, flags in any order, exit status 2 for usage errors; amended 2026-11-09: a wrongly paired remote daemon is a failed handshake; the completion scripts are run by the tests)
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
  - *Amendment 2026-11-09.* The tests run the scripts as well. Reading
    them could not show that bash 4 and later break the line at `:` and
    `=` (`COMP_WORDBREAKS`), so that everything after `--server
    host:5454` or `--tls-fingerprint sha256:...` completed nothing, or
    that zsh refused `--server=host:5454`. `TestBashCompletion` sources
    the script in every bash it finds and calls `_soma` with the words
    split both ways (the script must keep working on macOS's bash 3.2
    and without the bash-completion package); `TestZshCompletion`
    presses Tab in a zsh on a pty of its own
    (`cmd/soma/testdata/zsh-complete.zsh`). Both skip where the shell is
    missing, and both run in a sandbox whose `soma` is a stand-in that
    answers `completion channels` with the real helper's output and
    nothing else, so completing in the tests cannot reach a daemon or
    the user's cache.
- Machine-readable output is a convention: `status`, `list` and `favorite`
  take `--json`, and `status --json` never exits non-zero for a daemon
  that is not running or cannot be reached, so polling status bars keep
  parsing. A daemon that answers but fails the handshake (one speaking
  another protocol version, ADR-0006) is an error instead (amended
  2026-09-25): it may be playing, and reporting it as stopped was false.
  - *Amendment 2026-11-09.* That covers a remote daemon paired wrongly
    too. Every failure of `client.DialEndpoint` counted as "cannot be
    reached", including the ones after the daemon accepted the
    connection: a failed TLS handshake, another certificate than the
    pinned one, a rejected pre-shared key. `status --json` printed a
    stopped snapshot and exited 0 while that daemon played on, and the
    other commands told the user to go and run `soma daemon` there.
    Those failures are now a `client.HandshakeError`, which is an error
    for every command, and say what is likely wrong when the two ends
    disagree about TLS. Only a connection that could not be made at all
    still reads as stopped.
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
