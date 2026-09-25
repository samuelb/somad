# ADR-0008: PSK authentication is an HMAC challenge-response; the client always authenticates when a key is configured

- **Status:** Accepted
- **Date:** 2026-07-11 (client rule 2026-07-12)
- **Sources:** a396db0, de4cb38; `internal/protocol/auth.go`, `internal/client/client.go`; rejection of a per-address limiter on 2026-09-03

## Context

A pre-shared key is the simplest credential for a personal daemon, but it
must not travel over the wire, since TLS is a separate switch and the
Unix socket is unencrypted.

## Decision

- The server issues a random single-use nonce; the client answers with
  `HMAC-SHA256(psk, nonce)`; the server verifies in constant time. The key
  itself is never sent.
- A client with a PSK configured always runs the handshake, on every
  transport. "The server is the single source of truth on whether auth is
  required"; skipping it client-side based on the transport would let an
  endpoint that asks for auth silently connect without it.
- A failed attempt sleeps one second on that connection, then closes it.

## Consequences

- An eavesdropper on plaintext TCP cannot recover the key, only replay
  nothing (nonces are single-use).
- The PSK file must be private to the user (`security.CheckOwnerOnly`),
  and `soma daemon --gen-psk` writes a random key for the config template
  to point at (both e9964ab); neither existed when this was decided.

## Rejected alternatives

- **Shared rate limiter keyed on remote address** (2026-09-03). The
  per-connection delay is bypassed by opening another connection, but the
  challenge-response makes online key guessing against a decent key
  unrealistic. The actual threat is resource exhaustion, which the TCP
  connection cap and deadlines address (ce83966); a limiter map keyed by
  attacker-controlled addresses would itself need bounding.
