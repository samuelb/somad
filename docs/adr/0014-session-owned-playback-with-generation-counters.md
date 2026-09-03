# ADR-0014: One goroutine owns each playback session; the newest Play or Stop always wins

- **Status:** Accepted
- **Date:** 2026-06-19 (generation counters 2026-07-02, shared with the
  server 2026-09-03)
- **Sources:** 2ab8ba6, a0b1dc8, f9445e6; `internal/audio/player.go`

## Context

The audio player had data races the race detector could not see, because
the real player needs an audio device and is never exercised under
`-race`. A 250 ms fade loop also ran inline and froze keyboard input on
every stop or switch.

## Decision

- Playback is an immutable per-play "session". Exactly one goroutine
  (`runSession`) owns a session's oto player for its whole lifetime,
  including volume changes. `Play` and `Stop` only swap the current
  session under a mutex and return immediately.
- The old session fades out (250 ms) on its own goroutine while the new
  one fades in (500 ms), crossfading for gapless switching. On stop the
  context is cancelled before the pipe is closed so the resulting read
  error is suppressed instead of triggering a spurious reconnect.
- The server owns the single generation counter: it bumps it for every
  play, stop, and shutdown and passes it to `Play(url, format, gen)` and
  `Stop(gen)`. The player remembers the newest generation it has seen and
  refuses to commit an older one with `ErrSuperseded`; a `Stop` with an
  older generation is ignored so a stale stop cannot tear down a newer
  session, and one with the same generation stops exactly that session
  (the server reacting to its stream error). Retrying the same generation
  is allowed, which is how the server falls back to the next stream
  candidate. A session is not committed until decoding succeeds, the only
  synchronous failure mode.
- Errors (`StreamError`) and titles (`TrackInfo.Gen`) carry the generation
  of the session that produced them; the server drops reports whose
  generation is not the current one, so a stream still fading out during
  the crossfade can neither show its title under the new channel nor tear
  the new session down.
- The oto context is process-global, so at most one `AudioPlayer` per
  process. Device initialization is lazy and bounded by a 15 s wait.

## Consequences

- The TUI never blocks on audio (ADR-0003).
- Before the counters were shared (2026-09-03) the server and the player
  each kept their own, assigned at different moments, which left a window
  where a Stop between the server's check and `player.Play` let stale
  audio start under a stopped status, and a slow older play could commit
  after a newer one. With one counter the check inside the player is
  authoritative and the server's per-candidate check is only an early
  exit; the server never calls `Stop` on the post-commit supersede path,
  since the newer request either committed a replacement session or, when
  it failed to connect, stopped the player itself from `failConnect`.
- The server's `mockPlayer` in tests mirrors the generation rule so these
  races stay covered without an audio device.
