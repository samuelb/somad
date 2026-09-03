# ADR-0015: Track titles are demuxed from the playback connection, not fetched separately

- **Status:** Accepted
- **Date:** 2026-07-03
- **Sources:** 1ee8025, 1f9dec1; `internal/audio/metadata.go`

## Context

The first metadata reader opened a second HTTP connection every ten
seconds and downloaded a full `icy-metaint` block just to read one title,
roughly doubling traffic to SomaFM and lagging track changes by up to ten
seconds.

## Decision

- Request `Icy-MetaData: 1` on the playback connection and strip the
  interleaved metadata blocks out of the stream before they reach the
  decoder (`icyDemuxer`). One connection serves both audio and
  now-playing.
- The demuxer sits after the jitter buffer, so titles surface roughly
  when their audio is decoded, not when the network delivered them.
- `StreamTitle` is split on `';` rather than `;` so titles containing
  semicolons survive. The title is passed through unsplit as `Title`;
  there is no artist/title split yet.

## Consequences

- Track changes are near-instant and cost nothing extra upstream.
- Desktop notifications and scrobbling (wanted, TODO.md) both need an
  artist/title split; do it once and let MPRIS use it too.
