# ADR-0010: All outbound HTTP goes through a SomaFM host allowlist, with redirects re-validated and bodies capped

- **Status:** Accepted (amended 2026-09-03: a Last.fm allowlist entry; amended 2026-10-13: text from the network is sanitized before display)
- **Date:** 2026-02-18 (hardened through 2026-07-12)
- **Sources:** 8b9f5e4, cbdb7af, f2955b4, f1acbfa, 494330d, 04b1ad4, acdf2e5, 3a143ad; `internal/security/validation.go`, `securitytest/`

## Context

The daemon fetches the channel catalog, playlists and streams from URLs
it reads from SomaFM's API. Playlist and stream URLs are the one
attacker-influenceable input (through redirect targets), and a gosec
review flagged SSRF-shaped code paths.

## Decision

- Every outbound request is built through `security.NewRequest` /
  `ValidateURL`, which accepts only `somafm.com` and its subdomains
  (case-insensitive, suffix-anchored so `evilsomafm.com` fails), plus
  hosts tests register through the separate `securitytest` package, which
  lives outside `security` so the shipped binary never links `testing`.
- Redirects are re-validated hop by hop; net/http's default would follow
  ten hops to any host.
- One shared `http.Client` for connection reuse against the same few hosts.
- Response bodies are capped: 4 MiB for the catalog, 1 MiB for a playlist.
- Both `http` and `https` schemes are accepted for SomaFM hosts, because
  SomaFM's playlists list plain-http stream URLs.
- **2026-09-03:** Last.fm scrobbling (TODO.md) added `ws.audioscrobbler.com`
  as its own entry, `lastfmHosts`, consulted by `ValidateURL` alongside the
  SomaFM check rather than folded into it — exactly the "explicit second
  allowlist entry, not a widening" this ADR called for below. Unlike the
  SomaFM allowlist, it is https-only: there is no legacy plain-http stream
  URL to accommodate there, and the API carries session keys.
- **2026-10-13:** text from these responses that clients print (ICY
  titles, `songs.json` history, the catalog's names and descriptions)
  passes through `security.SanitizeText` where the daemon takes it in:
  line breaks become spaces, other control characters and invalid UTF-8
  are dropped. Clients print what the daemon sends verbatim, so an escape
  sequence injected into a plain-http stream would otherwise reach the
  user's terminal. Error text sent to clients (RPC errors, the stream and
  catalog errors in snapshots) and Last.fm's error messages are cleaned
  too, since errors quote network input such as a redirect target. New
  sources of displayed text sanitize the same way.

## Consequences

- Adding an integration that talks to another host requires an explicit
  second allowlist entry, not a widening of the check, as Last.fm scrobbling
  did above.
- Plain http means audio and ICY titles are MITM-able on hostile
  networks; since 5704e35 an https playlist or stream entry is preferred
  wherever the catalog offers one. A forged title can mislead but not
  control the terminal (see the 2026-10-13 entry above).

## Rejected alternatives

- **Path-traversal validation for cache and state paths** (removed
  2026-07-02, f1acbfa). Those paths are built from `os.UserCacheDir` /
  `os.UserHomeDir` plus constants, never from user input, so the check
  "added noise without protecting anything."
