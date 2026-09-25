# ADR-0006: Restart a version-skewed daemon only at moments that already interrupt playback

- **Status:** Accepted (amended 2026-09-25: a daemon speaking another protocol version)
- **Date:** 2026-07-05 (refined 2026-07-12, dev exemption added 2026-09-03)
- **Sources:** e5620c4, 9936858, 59fccbd, a396db0; `internal/client/spawn.go`,
  `internal/client/version.go`, `internal/client/peerpid*.go`; rejection of
  semver ordering on 2026-09-03

## Context

After an upgrade the running daemon is the old binary. The first attempt
restarted it on any command, "cutting off music just to upgrade the
daemon." The old binary still speaks the same protocol version, so there
is no urgency.

Across a protocol bump that no longer holds (v0.15.0 moved to protocol 2,
so a v0.14 daemon rejects the new client's hello). A daemon that rejects
hello also refuses every other request, `shutdown` included, so it could
be neither used nor stopped: even `soma daemon stop` failed, and
`status --json` reported it as stopped while it played on.

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
- A local daemon that rejects hello over the protocol version
  (`client.ProtocolSkewError`, recognized by the "incompatible protocol
  version" wording every daemon since the client-server split uses) gets
  the same moments (amended 2026-09-25). Play, next, prev, pause and an
  immediate stop replace it: the client reads the daemon's PID from the
  socket's peer credentials (`LOCAL_PEERPID` on macOS, `SO_PEERCRED` on
  Linux), sends it SIGTERM, which a daemon handles like a shutdown
  request, waits for it to release the socket, and spawns its own binary.
  `soma daemon stop` stops it the same way without the spawn. Everything
  else, the TUI included, fails with an error naming those commands:
  whether the daemon is playing cannot be asked, so it is left alone.
  `status --json` fails as well rather than report it as stopped. There
  is no fallback to the old daemon (it cannot be used) and no `dev`
  exemption (the pair cannot work together at all). A remote daemon is
  never signalled; a protocol mismatch there is a plain handshake error.

## Consequences

- Music is never cut off for an upgrade; the daemon upgrades itself at the
  next natural interruption.
- Two local installs — a dev build and a release — coexist without fighting
  over the daemon, at the cost of never auto-upgrading a dev daemon (or
  onto a dev client): that pairing is left running whatever it already is.
- After a protocol bump, passive commands and the TUI refuse to run until
  the user plays, pauses, or stops. `pause` cannot tell whether the old
  daemon was playing and assumes it was, so it leaves the fresh daemon
  stopped rather than risk starting music.
- On a platform without a peer-PID implementation (`peerpid_other.go`) a
  protocol-skewed daemon cannot be stopped from the CLI; the error says to
  quit it by hand.

## Rejected alternatives

- **Unconditional restart on skew** (reversed 2026-07-05, 9936858).
- **Semver ordering, restart only onto a newer client** (2026-09-03).
  Exempting `dev` fixes the real problem, two local installs fighting. The
  silent-downgrade case has never bitten anyone.
