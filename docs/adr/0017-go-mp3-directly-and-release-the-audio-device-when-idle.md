# ADR-0017: Decode MP3 with go-mp3 directly, and release the audio device when idle through an oto fork

- **Status:** Accepted
- **Date:** 2026-07-02 (device release 2026-07-13)
- **Sources:** dbaa748, 12045de, 0c0e7ba (rationale on branch commit ee50300), 073aa73; `internal/audio/player.go`, `resample.go`, `go.mod`

## Context

`ebiten/v2`, a full game engine, was a dependency solely for its
`audio/mp3` package, a thin resampling wrapper around `hajimehoshi/go-mp3`.
Separately, the daemon held the OS audio device open for its whole
lifetime; on macOS `coreaudiod` sat at about 11 % CPU with nothing
playing, because oto's `Suspend` only pauses the audio queue.

## Decision

- Use `go-mp3` directly plus a small linear resampler for the unlikely
  stream that is not at the oto context's 44.1 kHz. A zero source rate
  falls back to pass-through rather than repeating the first frame forever.
- Depend on the fork `github.com/samuelb/oto/v3`, which adds
  `SuspendAndRelease` (`AudioQueueStop`) without changing upstream
  `Suspend`/`Resume`. `player.go` imports the fork directly so re-vendoring
  preserves it. Measured `coreaudiod` while idle: about 0.5 %, down from 11 %.
- The wait for the audio backend to become ready is bounded at 15 s, so a
  hung ALSA daemon or broken device fails with a message instead of
  hanging the daemon before its socket is even useful. The timeout is not
  sticky: a later `Play` can use a recovered device.

## Consequences

- The dependency tree is much smaller. The fork must be carried forward
  when oto is updated; its only delta is the one method.
