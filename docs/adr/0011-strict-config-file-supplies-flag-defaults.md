# ADR-0011: The config file is strict, supplies flag defaults, and ships fully commented out

- **Status:** Accepted
- **Date:** 2026-07-07
- **Sources:** 4d177db; `internal/config/config.go`

## Context

Daemon flags such as `--idle-timeout` and `--no-tray` only applied when
the daemon was started by hand; the TUI and CLI auto-spawn it without
flags. Settings needed a home that reaches auto-spawned daemons too.

## Decision

- An optional YAML file supplies the defaults for flags; explicit flags
  still win. Every field is a pointer so "unset" is distinguishable from
  "explicitly zero".
- Parsing is strict: unknown keys, parse errors and invalid values stop
  the daemon with an error naming the line. "A typo (`idle_timout`) fails
  loudly instead of silently applying the default." An empty file is
  valid.
- Contradictory transport settings (`tls_cert` without `tls_key`, `psk`
  with `psk_file`, `tls_ca` with `tls_fingerprint`) are rejected at
  startup, not at connect time.
- On first start a template is written with `O_EXCL` and mode 0600, every
  setting commented out, so parsing it yields the built-in defaults even
  after those change, a user's file is never clobbered, and concurrent
  spawns cannot race.

## Consequences

- Adding a config key means adding it to the struct, the validator, and
  the template; the TODO's `quality` knob is an example.
- This is the deliberate opposite of how corrupt *state* and *cache* files
  are treated (ADR-0012): user-authored input fails loudly, machine-written
  files are moved aside.
