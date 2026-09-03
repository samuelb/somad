# ADR-0010: All outbound HTTP goes through a SomaFM host allowlist, with redirects re-validated and bodies capped

- **Status:** Accepted
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
- Both `http` and `https` schemes are accepted, because SomaFM's playlists
  list plain-http stream URLs.

## Consequences

- Adding an integration that talks to another host (Last.fm scrobbling is
  wanted, see TODO.md) requires an explicit second allowlist entry, not a
  widening of the check.
- Plain http means audio and ICY titles are MITM-able on hostile
  networks; preferring an https playlist entry where one exists is an
  open item in TODO.md.

## Rejected alternatives

- **Path-traversal validation for cache and state paths** (removed
  2026-07-02, f1acbfa). Those paths are built from `os.UserCacheDir` /
  `os.UserHomeDir` plus constants, never from user input, so the check
  "added noise without protecting anything."
