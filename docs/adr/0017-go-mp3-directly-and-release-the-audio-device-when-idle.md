# ADR-0017: Decode MP3 with go-mp3 directly, and suspend the audio device when idle

- **Status:** Accepted (amended 2026-09-04: the oto fork was never adopted)
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
- Suspend the oto context whenever no session is active and resume it on
  the next play, so the audio backend is not driven while nothing plays.
  This uses upstream `github.com/ebitengine/oto/v3` and its `Suspend`
  (`AudioQueuePause` on macOS).
  - *Amendment 2026-09-04.* The original record said the daemon depends on
    a fork, `github.com/samuelb/oto/v3`, adding `SuspendAndRelease`
    (`AudioQueueStop`) to fully release the device, measured at about 0.5 %
    idle `coreaudiod` instead of 11 %. That fork was never published
    (the repository does not exist) and the change lives only on the
    unmerged branch `release-audio-device-when-idle` (ee50300); `main` has
    always vendored upstream oto. The suspend-when-idle logic did land;
    the device-release half did not. Adopting a fork of the audio library
    remains a separate decision, to be taken by a new record if the idle
    CPU cost is found to matter.
- The wait for the audio backend to become ready is bounded at 15 s, so a
  hung ALSA daemon or broken device fails with a message instead of
  hanging the daemon before its socket is even useful. The timeout is not
  sticky: a later `Play` can use a recovered device.
  - *Amendment 2026-09-25.* oto 3.5 changed the Linux backend: it speaks
    the PulseAudio protocol natively (`jfreymuth/pulse`, pure Go, so
    PipeWire's pulse server works too) and falls back to ALSA, which it now
    loads at runtime through purego instead of linking with cgo. Builds no
    longer need `libasound2-dev`; the packages keep `libasound2`/`alsa-lib`
    as a runtime dependency for the fallback. The stream is named "Soma" in
    PulseAudio mixers (`ApplicationName`). On macOS, oto 3.5 rebuilds an
    AudioQueue that the system invalidated (for example when the output
    device changes) instead of going silent.

## Consequences

- The dependency tree is much smaller. No fork has to be carried forward
  when oto is updated.
