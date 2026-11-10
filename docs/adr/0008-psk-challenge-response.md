# ADR-0008: PSK authentication is an HMAC challenge-response; the client always authenticates when a key is configured

- **Status:** Accepted (amended 2026-11-09: the daemon warns about a short key)
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
  - *Amendment 2026-11-09.* "A decent key" was left to the user, and
    `server.psk: radio` was accepted without a word. Without the limiter,
    guessing is bounded only by the 64 connection slots and one attempt
    per connection with its one-second delay: about 64 guesses a second
    against an exposed TLS listener, which a word from a dictionary does
    not survive for long. The limiter stays rejected; instead the daemon
    warns about a key shorter than 16 bytes (`config.MinPSKLength`) and
    points at `soma daemon --gen-psk`, whose 64 hex digits pass. A short
    `server.psk` is a `Config.Warnings` entry, printed by the CLI and
    logged by the daemon like a config file that exposes its secrets; a
    short key in `--psk-file`/`server.psk_file` is logged by the daemon
    when it reads the file. It is a warning and never a refusal, so an
    upgrade cannot stop a daemon that worked the day before, and length
    is only a proxy for strength: sixteen bytes of a repeated letter
    pass. The client's copy (`client.psk`, `--psk-file`) is not checked:
    whoever holds it cannot change the daemon's key.
