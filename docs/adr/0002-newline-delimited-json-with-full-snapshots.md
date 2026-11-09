# ADR-0002: Newline-delimited JSON with full-state snapshots and an exact protocol version

- **Status:** Accepted (amended 2026-10-13: now at version 2; spectrum frames; the superseded error; amended 2026-11-09: playback snapshots carry a revision)
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
  - *Amendment 2026-11-09.* "Latest" was the last to arrive, and that is
    not always the newest. A reply and an event take different paths: the
    daemon builds the reply's snapshot under its lock but writes it only
    after the handler returns, which for a volume change or mute is after
    the state file is synced. A title change broadcast in between reached
    the client first, and the TUI then put the reply's older state back,
    showing the previous title until the next change. A playback snapshot
    now carries `revision`, which the daemon moves on whenever the state
    a snapshot shows has changed (`snapshotLocked` compares with the last
    one handed out, so no change can go unnumbered and an unchanged state
    keeps its number). A client drops a snapshot whose revision is lower
    than the one it holds (`PlaybackState.OlderThan`). The count belongs
    to one daemon process and starts at 1, so a client forgets the
    revision it holds when it reconnects, and takes no reply from the
    connection it replaced.
- `protocol.Version` must match **exactly** between client and server. It
  is bumped on any incompatible wire change; there is no negotiation. It
  has been 2 since v0.15.0 (61e6ebc, the mute toggle). Additions a client
  can do without, like the spectrum method and event, need no bump: an
  older daemon answers "unknown method" (ADR-0034).
  - *Amendment 2026-11-09.* The same goes for a field added to a
    payload, when either side works without it. `revision` is the first:
    an older client ignores it, as `encoding/json` does with any field it
    does not know, and an older daemon sends none, which decodes as 0.
    So 0 has to mean "no revision", never "the oldest": a client applies
    such snapshots in the order they arrive, as before.
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
