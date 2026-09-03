# ADR-0009: TLS 1.3 only, with an auto-generated long-lived self-signed certificate and three trust modes

- **Status:** Accepted
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
