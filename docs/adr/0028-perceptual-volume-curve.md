# ADR-0028: Volume is a linear percent on the wire and a cubic curve at the device

- **Status:** Accepted
- **Date:** 2026-09-03
- **Sources:** `internal/audio/player.go` `perceptualVolume`

## Context

The volume percent travelled unchanged from the CLI and TUI through the
server to `oto`, which multiplies samples by it. Loudness perception is
roughly a power law of amplitude, so a linear amplitude scale puts most of
the audible change in the bottom quarter of the range: 50 % sounded nearly
as loud as 100 %, and the useful settings were crowded between 0 and 25.

## Decision

- The wire protocol, the persisted state, the CLI, the TUI and MPRIS keep
  using the linear percent; `AudioPlayer.Volume()` returns it unchanged.
- `AudioPlayer` maps the target to amplitude with a cubic curve
  (`amplitude = target³`) at the single point where it hands a value to the
  oto player, and the fade-in and fade-out steps scale the linear target
  before running through the same curve, so fades are perceptually even.
- A fade-out starts from the amplitude the player is actually at (mapped
  back with a cube root), not from the global target, because a stop can
  land in the middle of a fade-in.

## Consequences

- Existing saved volumes sound quieter after the upgrade: a stored 50 %
  now plays at 12.5 % amplitude. Users turn it up once.
- The curve is invisible on the wire, so remote clients and scripts are
  unaffected.

## Rejected alternatives

- An exponential (decibel) curve: a true dB mapping needs a floor
  (silence is −∞ dB) and a chosen range; the cubic approximation has no
  parameters and is the common choice in players.
- Applying the curve in the clients: every client would have to agree, and
  the persisted state would become ambiguous.
