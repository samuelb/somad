# ADR-0007: The Unix socket is authorized by file permissions; non-loopback TCP requires TLS and a PSK

- **Status:** Accepted
- **Date:** 2026-07-11 (tightened 2026-07-12)
- **Sources:** a396db0, 890fd5f; `internal/server/conn.go`, `cmd/soma/main.go` `checkTCPSecurity`, `internal/protocol/auth.go`

## Context

The daemon controls playback and can be shut down remotely. Locally, the
socket directory is `0700` and owner-checked (ADR-0004), so the machine's
user boundary already limits who can connect. Over TCP nothing does. The
first TCP transport shipped with auth "off by default with loud warnings";
that left "full playback control (including remote shutdown) open to
anyone who can reach the port," and a PSK over plaintext authenticated
only the handshake, so an on-path attacker could hijack the session.

## Decision

- Unix-socket connections are exempt from authentication. The directory
  permission check is the authorization.
- A non-loopback TCP listener **refuses to start** unless both TLS
  (ADR-0009) and a PSK (ADR-0008) are configured. `--insecure` or
  `server.insecure` restores the old behaviour explicitly, with prominent
  startup warnings.
- Loopback TCP binds only warn: "the machine boundary already limits
  exposure, like the Unix socket."
- Unauthenticated connections stay unregistered: they receive no state
  broadcasts and never hold off the idle exit (ADR-0005). The auth success
  response is sent only after the connection is registered, so a client
  acting on it cannot miss a broadcast.

## Consequences

- Remote control needs a one-time pairing: generate the certificate, copy
  the fingerprint, share the key.
- The TCP path still lacks read deadlines and a connection cap, so an
  unauthenticated peer can hold resources open (P1 item in TODO.md).
