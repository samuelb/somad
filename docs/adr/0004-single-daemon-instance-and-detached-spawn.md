# ADR-0004: One daemon instance via a lock file; clients spawn it detached

- **Status:** Accepted (amended 2026-09-25: clients act on the spawned daemon's exit status; amended 2026-11-09: what a daemon writes before it holds the lock, and stopping a daemon waits for its lock)
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
  - *Amendment 2026-11-09.* A client that stops a daemon waits for the
    lock, not just for the socket. A daemon shutting down answers the
    request, closes its listeners, and releases the lock only after
    flushing its state and waiting up to 3 s for a Last.fm submission
    still under way. `soma daemon stop` returned at the answer, so `soma
    daemon stop && soma daemon` could find the lock held, log "already
    running", and exit 0 with no daemon left. `client.StopServer` and
    the version-skew restart (ADR-0006) now wait, up to 5 s, until the
    socket no longer answers and the lock is free, and `soma daemon
    stop` fails if the daemon has not let go by then. The only way to
    ask whether a `flock` is held is to take it, so the client takes a
    shared lock and drops it at once; a daemon starting in that instant
    would see "already running", which the spawning client's retry
    below covers. The daemon's process lives on for the 400 ms of its
    fade-out after the lock is free; nothing waits for that. A remote
    daemon cannot be watched and is not waited for.
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
  - *Amendment 2026-11-09.* One step was not. A daemon resolves its
    options, and with TLS enabled generates its certificate, before it
    takes the lock, and two started at once could leave a key and a
    certificate that did not belong together. Generating the pair has a
    lock of its own now (ADR-0009). Anything else a daemon writes before
    `server.Listen` has to be safe to write twice at once in the same
    way; the config template and a generated PSK already are, through
    `atomicfile.CreateExclusive`.
- The spawning client waits on the daemon's exit status (amended
  2026-09-25), so the daemon must keep it meaningful: "already running"
  exits 0, and the client spawns again a second later in case the lock
  holder was only shutting down; any failure to start exits non-zero,
  which ends the client's wait at once with the server log's tail instead
  of re-spawning into the same failure until the 15 s timeout.
- The Unix socket is trusted precisely because of the directory check, so
  that check must never be weakened.
