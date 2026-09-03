# ADR-0006: Restart a version-skewed daemon only at moments that already interrupt playback

- **Status:** Accepted
- **Date:** 2026-07-05 (refined 2026-07-12)
- **Sources:** e5620c4, 9936858, 59fccbd, a396db0; `internal/client/spawn.go`; rejection of semver ordering on 2026-09-03

## Context

After an upgrade the running daemon is the old binary. The first attempt
restarted it on any command, "cutting off music just to upgrade the
daemon." The old binary still speaks the same protocol version, so there
is no urgency.

## Decision

- `EnsureServer` leaves a skewed-but-playing daemon alone. Passive commands
  (volume, favorite, list, status) and browsing the TUI never restart it.
- `EnsureServerForPlayback` restarts it, because the caller (channel
  change, pause, stop) interrupts the stream regardless. A restart that
  times out falls back to whatever answers: "the user's command outranks
  the upgrade."
- Remote (`--server`) endpoints are never spawned and never restarted; an
  unreachable one is an error and a skewed one is left alone.

## Consequences

- Music is never cut off for an upgrade; the daemon upgrades itself at the
  next natural interruption.
- Skew is detected by string inequality, so a `go build` dev binary and an
  installed release restart the daemon onto each other. Exempting the
  `dev` version is the planned fix (TODO.md).

## Rejected alternatives

- **Unconditional restart on skew** (reversed 2026-07-05, 9936858).
- **Semver ordering, restart only onto a newer client** (2026-09-03).
  Exempting `dev` fixes the real problem, two local installs fighting. The
  silent-downgrade case has never bitten anyone.
