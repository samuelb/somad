# ADR-0002: Newline-delimited JSON with full-state snapshots and an exact protocol version

- **Status:** Accepted (amended 2026-10-13: now at version 2; spectrum frames; the superseded error)
- **Date:** 2026-07-05
- **Sources:** 95fb723; `internal/protocol/protocol.go`, `codec.go`; rejection of version negotiation on 2026-09-03

## Context

The daemon split (ADR-0001) needs a wire format that a TUI, a CLI and
tests can all speak easily, and that a stateless UI can render from.

## Decision

- Newline-delimited JSON over a Unix domain socket, optionally TCP.
  Clients send `Request`s; the server answers with ID-correlated
  `Response`s and pushes `Event`s.
- State and channels events always carry a **full snapshot**, never
  deltas, so a client can render from the latest event alone (ADR-0003).
  Spectrum events (ADR-0034), sent only to subscribed connections, carry
  the latest frame of levels instead; each one is just as
  self-contained.
- `protocol.Version` must match **exactly** between client and server. It
  is bumped on any incompatible wire change; there is no negotiation. It
  has been 2 since v0.15.0 (61e6ebc, the mute toggle). Additions a client
  can do without, like the spectrum method and event, need no bump: an
  older daemon answers "unknown method" (ADR-0034).
- One `Write` call per line so concurrent writers never interleave partial
  lines. The line limit is 4 MiB because the channel catalog travels as one
  JSON line.
- `Response.Error` is free text for people, with one exception
  (2026-10-13): `protocol.ErrSuperseded`, the answer to a play a newer
  play or stop overtook, is sent verbatim and returned as that sentinel by
  the client, so the TUI can tell it from a real failure and stay silent.

## Consequences

- Protocol changes are all-or-nothing upgrades, which is acceptable because
  both ends are always the same binary and local skew is handled by
  restarting the daemon (ADR-0006).
- The 4 MiB budget exists for the server-to-client catalog; the server's
  own read side is capped much lower (`protocol.MaxRequestBytes`, ce83966).

## Rejected alternatives

- **Min/max protocol version range in hello** (2026-09-03). At the time
  the protocol was at version 1, and negotiation for an incompatible
  change that had never happened was speculative. The move to version 2
  came the same day and did not reopen it: a local daemon of the other
  version is replaced at the next interruption (ADR-0006, amended
  2026-09-25), and a remote one fails with a clear error, so a range
  would only spare a remote user the upgrade.
