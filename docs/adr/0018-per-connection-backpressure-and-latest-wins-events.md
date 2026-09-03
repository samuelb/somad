# ADR-0018: Per-connection request cap and latest-wins event delivery

- **Status:** Accepted
- **Date:** 2026-07-05 (shutdown-aware 2026-07-12)
- **Sources:** 48dff39, f0d3eb4, 88312ec; `internal/server/conn.go`

## Context

Every request line spawned its own goroutine with no limit, and a slow
client could in principle block the broadcast path for everyone.

## Decision

- A 32-slot semaphore caps concurrent dispatch per connection, so a client
  sending faster than the server handles applies backpressure to its own
  read loop. During teardown the read loop bails out instead of waiting
  for a slot.
- Events are delivered through single-slot, latest-wins channels per event
  type. A slow client only ever loses intermediate snapshots; it can never
  block the server's broadcast. This works because events are full
  snapshots (ADR-0002).
- Responses and events share a write mutex so lines never interleave; a
  failed write tears the connection down.

## Consequences

- The server has no per-client queue to grow. Delta events would break
  the latest-wins property and must not be introduced.
- The cap is per connection; a cap on the number of connections is a
  separate open item (TODO.md, TCP hardening).
