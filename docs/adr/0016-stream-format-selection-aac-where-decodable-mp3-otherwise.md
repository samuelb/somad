# ADR-0016: Prefer AAC where the platform decodes it, MP3 elsewhere, always the highest quality; no Linux AAC decoder

- **Status:** Accepted
- **Date:** 2026-09-02 (quality ranking 2026-07-02; Linux AAC declined 2026-09-03)
- **Sources:** 477e879, 6005115, 591c434; `internal/audio/decoder.go`, `aac_darwin.go`, `aac_other.go`, `internal/channels/select.go`

## Context

SomaFM offers each channel as MP3 and AAC at several quality labels.
"SomaFM's 128 kbps AAC-LC streams sound noticeably better than the MP3
streams at the same bitrate." Picking the first playlist entry made
quality depend on API ordering.

## Decision

- `PreferredFormats` returns AAC then MP3 where the build can decode AAC,
  MP3 only otherwise. Candidates are tried in order; if the preferred
  stream fails to connect or decode, the next one is tried.
- AAC decoding uses the macOS AudioToolbox converter through cgo
  (`aac_darwin.go`), behind a pure-Go ADTS reader that validates headers
  strictly and resynchronizes on the syncword. That strictness is
  load-bearing: AudioToolbox conceals corrupt payloads rather than
  erroring, so the ADTS parse is the real "is this AAC?" check.
- HE-AAC (`aacp`) playlists are never selected: decoding them as AAC-LC
  would silently drop the SBR band.
- Within a format the highest quality label wins. The rank seed sits above
  the unknown rank so a channel with only unrecognized labels still picks
  something.
- The wire protocol was deliberately left unchanged by AAC support.

## Consequences

- On Linux the candidate list is MP3 only, so the runtime fallback never
  runs there; the README's fallback sentence is macOS-only.
- Platform-conditional decoders follow the `_darwin.go` / `_other.go`
  build-tag pair convention; keep both sides in sync.

## Rejected alternatives

- **AAC decoding on Linux** (declined 2026-09-03). It "would mean a new
  cgo library dependency" (fdk-aac or faad2) with fallout in CI, deb/rpm
  depends, brew, nix and the PKGBUILD. AAC is only worth having where the
  platform ships a decoder.
