# ADR-0007: The Unix socket is authorized by file permissions; non-loopback TCP requires TLS and a PSK

- **Status:** Accepted
- **Date:** 2026-07-11 (tightened 2026-07-12, resource limits 2026-09-03)
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
- TCP connections are resource-bounded; the Unix socket is not, since its
  directory already restricts it to the owning user:
  - a 10 s handshake deadline covers the lazy TLS handshake, the PSK
    exchange, and hello, and is cleared once hello succeeds. There is no
    read deadline after that: an idle TUI legitimately sends nothing for
    hours, and dead peers are reaped by TCP keepalive, which the listener
    enables by default;
  - every write carries a 30 s deadline, so a peer that stops reading is
    dropped instead of parking the writer on the write mutex;
  - the server reads requests with a 64 KiB line cap
    (`protocol.MaxRequestBytes`); the 4 MiB budget is only needed for the
    server-to-client catalog event and stays on the client's scanner;
  - the TCP listener is wrapped in `server.LimitListener` with a cap of
    64 open connections; further peers wait in the listen backlog.

## Consequences

- Remote control needs a one-time pairing: generate the certificate, copy
  the fingerprint, share the key.
- An unauthenticated peer can hold at most one of 64 slots for 10 s, and
  a 64 KiB scanner buffer, before it is dropped. The per-connection 1 s
  auth-failure delay stays per connection (ADR-0008 rejects a shared
  per-address limiter); the cap and deadlines bound what opening many
  connections can cost.
