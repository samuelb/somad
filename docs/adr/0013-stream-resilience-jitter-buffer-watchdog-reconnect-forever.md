# ADR-0013: Stream resilience: jitter buffer, stall watchdog, clean EOF is an error, reconnect forever

- **Status:** Accepted
- **Date:** 2026-07-05 (jitter buffer 2026-09-01)
- **Sources:** eead2e1, d39497b, de8dd79, f062394, 1f9dec1; `internal/audio/player.go`, `buffer.go`; earlier ae9f67c, 4ee15a3, 4f3332d; rejection of watchdog re-arm on 2026-09-03

## Context

The daemon is long-running. Overnight suspends, router reboots and NAT
timeouts all happen, and a live radio stream never ends on its own. An
unbuffered pipe fed the decoder at exactly the network's pace, so any
hiccup longer than the audio device's own small buffer was audible.

## Decision

- A 512 KiB jitter buffer (about half a minute at 128 kbps) sits between
  the fetch and the decoder, with a 32 KiB prefill bounded by a one-second
  deadline. After an underrun reads resume as soon as any byte arrives,
  "trading a possible stutter for the shortest dropout."
- A 30 s no-data watchdog aborts a connection that died without a FIN. It
  is armed before the request so a server that never answers is caught.
- A clean EOF is treated as an error: "a live stream never ends on its
  own; a clean EOF means the server hung up."
- Reconnection backs off exponentially to a one-minute cap and then
  retries forever. Only an explicit stop or a new play ends it. A missing
  playlist is never retried, because reconnecting cannot conjure one up.
- Each failure has exactly one reporting path, and the errors channel is
  lossy by design: it signals "currently unhealthy", it is not a log.

## Consequences

- A network drop plays out the buffered audio before the error surfaces,
  and ICY titles align with what is heard (ADR-0015).
- The watchdog only covers the network side. A decoder error after `Play`
  returns leaves the buffer full and the fill goroutine parked, so it is
  invisible (P1 item in TODO.md).

## Rejected alternatives

- **Custom ring buffer** (2026-01-31, replaced by `bufio` on 2026-02-03,
  which was in turn replaced by the dedicated jitter buffer above).
- **A budget of five reconnect attempts** (reversed 2026-07-05): "any
  longer outage meant the music never came back."
- **Re-arming the watchdog on buffer consumption** (2026-09-03). The
  decoder-error reader wrapper is the fix for the gap above; this was
  only an optional extra guard.
