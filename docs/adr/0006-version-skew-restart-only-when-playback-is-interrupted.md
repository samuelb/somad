# ADR-0006: Restart a version-skewed daemon only at moments that already interrupt playback

- **Status:** Accepted
- **Date:** 2026-07-05 (refined 2026-07-12, dev exemption added 2026-09-03)
- **Sources:** e5620c4, 9936858, 59fccbd, a396db0; `internal/client/spawn.go`,
  `internal/client/version.go`; rejection of semver ordering on 2026-09-03

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
- Skew is plain string inequality (`client.VersionSkewed`, one exported
  helper shared by `internal/client/spawn.go`, `cmd/soma/cli.go`, and
  `Model.skewed` in `internal/app/model.go`), with one exemption: `"dev"`
  on either side never counts as skew. A `go build` dev binary and an
  installed release running on the same machine would otherwise restart
  the daemon onto each other on every channel change.

## Consequences

- Music is never cut off for an upgrade; the daemon upgrades itself at the
  next natural interruption.
- Two local installs — a dev build and a release — coexist without fighting
  over the daemon, at the cost of never auto-upgrading a dev daemon (or
  onto a dev client): that pairing is left running whatever it already is.

## Rejected alternatives

- **Unconditional restart on skew** (reversed 2026-07-05, 9936858).
- **Semver ordering, restart only onto a newer client** (2026-09-03).
  Exempting `dev` fixes the real problem, two local installs fighting. The
  silent-downgrade case has never bitten anyone.
