# ADR-0016: Prefer AAC where the platform decodes it, MP3 elsewhere, at the configured quality; no Linux AAC decoder

- **Status:** Accepted (amended 2026-09-25: HE-AAC is decoded and selected by the quality setting)
- **Date:** 2026-09-02 (quality ranking 2026-07-02; Linux AAC declined 2026-09-03; quality knob 2026-09-03; HE-AAC decoding and selection 2026-09-25)
- **Sources:** 477e879, 6005115, 591c434, e075e44; `internal/audio/decoder.go`, `aac_darwin.go`, `aac_other.go`, `internal/channels/select.go`

## Context

SomaFM offers each channel as MP3 and AAC at several quality labels.
"SomaFM's 128 kbps AAC-LC streams sound noticeably better than the MP3
streams at the same bitrate." Picking the first playlist entry made
quality depend on API ordering.

## Decision

- `PreferredFormats` returns AAC then MP3 where the build can decode AAC,
  MP3 only otherwise (AAC, HE-AAC, MP3 since 2026-09-25). Candidates are
  tried in order; if the preferred stream fails to connect or decode on
  every mirror (ADR-0013), the next one is tried.
- AAC decoding uses the macOS AudioToolbox converter through cgo
  (`aac_darwin.go`), behind a pure-Go ADTS reader that validates headers
  strictly and resynchronizes on the syncword. That strictness is
  load-bearing: AudioToolbox conceals corrupt payloads rather than
  erroring, so the ADTS parse is the real "is this AAC?" check.
- HE-AAC (`aacp`) playlists are never selected: decoding them as AAC-LC
  would silently drop the SBR band. *Reversed 2026-09-25* by the decoder
  change below: `aacp` is now a format like the others.
- **2026-09-25:** the converter's format comes from the stream, not from
  the ADTS header. SBR (HE-AAC) and parametric stereo (HE-AAC v2) are
  signalled only inside the payload, and the header describes the AAC-LC
  core at half the output rate. The premise above was wrong: 31 of 46
  SomaFM `aac` playlists (the 128 kbps "highest" ones) are HE-AAC with a
  22.05 kHz core, and were decoded as that core alone, so they played
  band-limited to 11 kHz. The first frames now go through the system ADTS
  parser (`AudioFileStream`), which inspects the payload and reports the
  richest decodable format (AAC-LC, HE-AAC, HE-AAC v2) and a magic cookie
  for the converter; output is always 16-bit stereo at the stream's real
  rate. Hand-building the format from the header was tried and rejected:
  AudioToolbox needs the cookie for HE-AAC v2, and without one it crashed
  inside the converter on a mono-core stream.
- Within a format the highest quality label wins. The rank seed sits above
  the unknown rank so a channel with only unrecognized labels still picks
  something.
  - *Amendment 2026-09-25.* e075e44 (2026-09-03) added `server.quality` /
    `--quality` without amending this record: within a format the label
    closest to the preferred quality wins. It had no effect, because SomaFM
    offers MP3 and AAC at `highest` only and its `high` (about 64 kbps) and
    `low` (about 32 kbps) streams only as `aacp`, which was never selected,
    so every channel fell back to `highest`. Now candidates are ordered by
    closeness to the preferred quality first and by format preference only
    among equally close ones, so `high` and `low` pick the `aacp` playlists
    where the build decodes them. Without AAC decoding (Linux) the setting
    still cannot change anything; the daemon logs a note at start when a
    lower quality is set there.
- The wire protocol was deliberately left unchanged by AAC support.

## Consequences

- On Linux the candidate list is MP3 only, so the runtime fallback never
  runs there; the README's fallback sentence is macOS-only. So is
  `server.quality`, for as long as SomaFM offers MP3 at `highest` only.
- On macOS a play attempt can have three candidates (AAC, HE-AAC, MP3),
  which the client's play-call timeout has to cover (ADR-0013).
- Platform-conditional decoders follow the `_darwin.go` / `_other.go`
  build-tag pair convention; keep both sides in sync.

## Rejected alternatives

- **AAC decoding on Linux** (declined 2026-09-03). It "would mean a new
  cgo library dependency" (fdk-aac or faad2) with fallout in CI, deb/rpm
  depends, brew, nix and the PKGBUILD. AAC is only worth having where the
  platform ships a decoder.
