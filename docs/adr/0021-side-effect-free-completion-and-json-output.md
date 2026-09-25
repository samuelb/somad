# ADR-0021: Shell completion is cache-only and side-effect free; scripting uses `--json`

- **Status:** Accepted (amended 2026-09-25: status --json fails on a failed handshake)
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

## Consequences

- Every new client command should accept `--json`; `play`, `next`,
  `prev`, `pause`, `stop` and `volume` do not yet (TODO.md).
