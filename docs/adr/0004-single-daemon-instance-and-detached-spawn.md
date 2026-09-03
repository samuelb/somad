# ADR-0004: One daemon instance via a lock file; clients spawn it detached

- **Status:** Accepted
- **Date:** 2026-07-05
- **Sources:** 95fb723; `internal/server/spawnlock.go`, `internal/client/spawn.go`, `internal/protocol/socket.go`

## Context

Several clients may notice "no daemon" at the same moment and try to
start one. The daemon must outlive the terminal that spawned it, and a
stale socket file from a crashed daemon must not block the next start.

## Decision

- The server takes a `flock` on a lock file beside the socket. A second
  server gets `ErrAlreadyRunning` and exits cleanly; the clients that
  spawned both end up connecting to the winner. Only the lock holder
  removes a stale socket file.
- Clients spawn the daemon with `Setsid` and `context.Background()` so it
  is detached from the terminal and never cancelled by the client.
- The socket path is kept deliberately short (macOS caps `sun_path` at
  104 bytes) and resolves `$SOMAD_SOCKET`, then `$XDG_RUNTIME_DIR`, then a
  per-uid temp dir. The socket directory is created `0700` and its owner
  and mode are verified on every start, because that check is the entire
  authorization story for local clients (ADR-0007).
- The daemon's log file is capped and truncated on spawn so it cannot grow
  forever.

## Consequences

- Concurrent auto-spawns are harmless by construction; tests rely on it.
- The Unix socket is trusted precisely because of the directory check, so
  that check must never be weakened.
