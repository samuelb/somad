# ADR-0014: One goroutine owns each playback session; the newest Play or Stop always wins

- **Status:** Accepted
- **Date:** 2026-06-19 (generation counters 2026-07-02)
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
- Every `Play`/`Stop` bumps a generation counter. A connect that finishes
  after a newer request backs out with `ErrSuperseded` instead of
  committing: the newer request owns the audio state. A session is not
  committed until decoding succeeds, the only synchronous failure mode.
- The oto context is process-global, so at most one `AudioPlayer` per
  process. Device initialization is lazy and bounded by a 15 s wait.

## Consequences

- The TUI never blocks on audio (ADR-0003).
- The server keeps its own generation counter, assigned before the player
  is called, and the two are not coupled. That leaves a window where a
  Stop between the server's check and `player.Play` lets stale audio
  start, and a slow older play can commit after a newer one. Merging the
  counters is a P1 item in TODO.md and also gives `TrackInfo` a
  generation to filter crossfade-window titles by.
