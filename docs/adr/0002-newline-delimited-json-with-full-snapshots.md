# ADR-0002: Newline-delimited JSON with full-state snapshots and an exact protocol version

- **Status:** Accepted
- **Date:** 2026-07-05
- **Sources:** 95fb723; `internal/protocol/protocol.go`, `codec.go`; rejection of version negotiation on 2026-09-03

## Context

The daemon split (ADR-0001) needs a wire format that a TUI, a CLI and
tests can all speak easily, and that a stateless UI can render from.

## Decision

- Newline-delimited JSON over a Unix domain socket, optionally TCP.
  Clients send `Request`s; the server answers with ID-correlated
  `Response`s and pushes `Event`s.
- Events always carry a **full state snapshot**, never deltas, so a client
  can render from the latest event alone (ADR-0003).
- `protocol.Version` must match **exactly** between client and server. It
  is bumped on any incompatible wire change; there is no negotiation.
- One `Write` call per line so concurrent writers never interleave partial
  lines. The line limit is 4 MiB because the channel catalog travels as one
  JSON line.

## Consequences

- Protocol changes are all-or-nothing upgrades, which is acceptable because
  both ends are always the same binary and local skew is handled by
  restarting the daemon (ADR-0006).
- The 4 MiB budget exists for the server-to-client catalog; the server's
  own read side should be capped much lower (open item in TODO.md, TCP
  hardening).

## Rejected alternatives

- **Min/max protocol version range in hello** (2026-09-03). The protocol is
  at version 1 and there is no v2. Negotiation for an incompatible change
  that has never happened is speculative; revisit only when such a change
  is actually made.
