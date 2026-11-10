# ADR-0009: TLS 1.3 only, with an auto-generated long-lived self-signed certificate and three trust modes

- **Status:** Accepted (amended 2026-09-25: a hint on hostname mismatch instead of regenerating; amended 2026-11-09: generated under a lock, and a pair that does not load is replaced)
- **Date:** 2026-07-11 (TLS 1.3 floor 2026-09-01)
- **Sources:** a396db0, 4c1cdaf; `internal/tlsutil/tlsutil.go`

## Context

Both ends of the TCP transport are always the `soma` binary. There is no
browser, no legacy peer, and no public identity to prove; the certificate
is a pairing credential for a personal music daemon.

## Decision

- `MinVersion` is TLS 1.3 on both server and client. Go has negotiated 1.3
  by default since 1.14, so the change only removed the ability to
  downgrade.
- When no certificate is configured the server generates a self-signed one
  into the state dir once, with a ten-year validity, marked as its own CA
  and backdated an hour for clock skew. Expiry "would only break the
  user's setup." The key is written before the certificate so a failed
  second write leaves a harmless leftover.
  - *Amendment 2026-11-09.* "Once" did not hold for two daemons started
    at the same moment. The pair is prepared before a daemon takes its
    single-instance lock (ADR-0004), so on the first start with TLS both
    found no files and both wrote a key and then a certificate.
    Interleaved, that left one daemon's key beside the other's
    certificate, which does not load and, with both files there, was
    never replaced: the daemon could not start until they were deleted
    by hand. Or the second pair replaced the first after its daemon had
    loaded it, and the fingerprint served was not the one in the files
    or from `--show-cert`. `EnsureServerCert` now checks and generates
    under a `flock` on `<certificate>.lock`, so the second caller waits
    and finds the pair the first wrote. The lock file stays behind, like
    the daemon's own. Where it cannot be created (a state directory that
    is read-only), a pair that loads is still used and none is
    generated.
  - *Amendment 2026-11-09.* A key and a certificate that both exist but
    do not load as a pair are moved to `.corrupt` (ADR-0012) and a new
    pair is generated, with a warning in the log that pinned clients need
    the new fingerprint. Such a pair is what the race above left behind,
    and what a failed second write leaves next to a stale certificate.
    This does not go against the rule below that the certificate is not
    regenerated: the rule protects clients paired with a certificate the
    daemon serves, and a pair that does not load cannot be served, so no
    pairing is left to break, while keeping it stopped a daemon set to
    listen with TLS from starting at all, and local playback with it. A
    pair that loads is never replaced, whatever names it lacks.
- Clients trust it one of three mutually exclusive ways: a CA file, a
  pinned SHA-256 fingerprint, or system roots. Pinning uses
  `VerifyConnection` so resumed sessions are checked too; it replaces chain
  verification rather than skipping it. Fingerprint pinning is "the
  copy-paste-simple path for auto-generated certificates."

## Consequences

- Pairing is: `soma daemon --show-cert` on the server, paste the
  fingerprint into the client config.
- Contradictory trust settings are rejected at startup, not at connect
  time (ADR-0011).
- The generated certificate names only `localhost`, the loopback
  addresses, the hostname, and a specific `--listen` host, so trusting it
  as a CA file fails when a client dials the server by a LAN address
  (amended 2026-09-25). It is not regenerated to add names, which would
  break every client pinning its fingerprint; instead the client's
  hostname-mismatch error lists the names the certificate covers and
  points at fingerprint pinning (`tlsutil.ExplainHostnameMismatch`).
