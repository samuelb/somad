# ADR-0015: Track titles are demuxed from the playback connection, not fetched separately

- **Status:** Accepted
- **Date:** 2026-07-03 (artist/title split 2026-09-03)
- **Sources:** 1ee8025, 1f9dec1, 27582b6; `internal/audio/metadata.go`

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
  semicolons survive. The title is passed through unsplit as `Title`,
  and the wire protocol carries it that way.
- **2026-09-03** (27582b6): consumers that need an artist split the raw
  title with `audio.SplitTitle`, on the first `" - "`. A title without
  the separator (common on genre and ambient channels) yields no artist,
  and each consumer picks its own fallback: MPRIS shows the channel name
  as the artist, a desktop notification puts only the channel in its
  body, and Last.fm skips the track, since a scrobble needs an artist.

## Consequences

- Track changes are near-instant and cost nothing extra upstream.
- Desktop notifications and scrobbling both needed an artist/title split;
  it is done once, in `SplitTitle`, and MPRIS uses it too.
