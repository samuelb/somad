# ADR-0038: A daemon run by a service manager is restarted through it, not replaced by a spawn

- **Status:** Accepted
- **Date:** 2026-11-10
- **Sources:** the 2026-11 review; `cmd/soma/daemon.go` (`--supervised`, `restartExitStatus`), `internal/server/server.go` (`ShutdownForRestart`, `RestartRequested`), `internal/protocol/types.go` (`HelloResult.Supervised`, `ShutdownParams`), `internal/client/spawn.go` (`Restart`, `AwaitRestart`), `cmd/soma/tui.go` (`reconnect`), `packaging/systemd/soma.service`, `packaging/launchd/com.samuelb.soma.plist`

## Context

A client restarts a local daemon by sending `shutdown` and spawning a
detached `soma daemon` of its own (ADR-0004, ADR-0006). The repository also
ships a systemd user unit and a launchd agent that run the daemon as a
service, both restarting it only after a failure. A daemon shut down by a
client exits with status 0, so the service manager took it for stopped on
purpose, and the daemon the client spawned ran on outside the service:
without the flags of the unit's `ExecStart` (`--listen`, `--no-tray`), with
its log in `server.log` instead of the journal, and in the way of the
unit, whose next start found a daemon running and exited at once. The
first `soma play` after every package upgrade did this.

## Decision

- **The daemon is told that it is supervised**: `soma daemon --supervised`,
  which the shipped unit and agent pass. There is no config key: an
  auto-spawned daemon reads the same config file and is not supervised.
  Nor is it guessed from the environment (`INVOCATION_ID`,
  `XPC_SERVICE_NAME`), which every process started from a service
  inherits, a daemon spawned from a terminal that runs as one included.
- **It tells its clients**, in `HelloResult.Supervised`.
- **A restart goes through the service manager.** A client that restarts
  the daemon sends `shutdown` with `ShutdownParams.Restart`. A supervised
  daemon then exits with status 75 (`EX_TEMPFAIL`) once it has shut down,
  which `Restart=on-failure` and launchd's `KeepAlive` with
  `SuccessfulExit` false answer by starting it again; the systemd unit
  also lists 75 as a success that forces a restart, so the journal shows no
  failure. The client waits for the new instance (`client.AwaitRestart`)
  and does not spawn. The TUI, which restarts through its reconnect, does
  the same when the connection it lost was to a supervised daemon.
- **A daemon on its own ignores the wish** and exits with 0, as does any
  daemon that predates the field, and the client spawns as before. So the
  client always asks; only the daemon knows who runs it. Both fields are
  additions, and `protocol.Version` stays (ADR-0002).
- **The user's command still outranks supervision.** If the service
  manager has not brought the daemon back within 12 s
  (`supervisedRestartWait`: systemd takes a fraction of a second, launchd
  holds a job that ran for less than ten seconds back until they have
  passed), the client spawns one after all.
- **`soma daemon stop` stops it for good**: a bare `shutdown`, status 0,
  no restart.

## Consequences

- An upgrade, and a lost audio device (ADR-0037), leave a packaged daemon
  under systemd or launchd, with its flags.
- A unit written by hand has to pass `--supervised` and restart on status
  75, or it behaves as before. An installed copy of the launchd agent is
  the user's file and has to be updated by hand; the packaged systemd unit
  is replaced by the package, but the daemon it started before the
  upgrade knows nothing of the flag, so the first restart after upgrading
  to this version still spawns.
- A daemon that speaks another protocol version cannot be asked for
  anything and is stopped with SIGTERM (ADR-0006), which it answers with
  status 0: after a protocol bump a supervised daemon is replaced by a
  spawn once more.
- Between the old daemon's exit and the service manager's restart, a
  command from another terminal finds no daemon and spawns one; the
  service's instance then exits as "already running". The window is the
  restart delay, and the result is the state before this record.

## Rejected alternatives

- **`Restart=always` in the units.** `soma daemon stop` would no longer
  stop the daemon, and a client's spawn would race every restart.
- **Have the supervised instance wait for the lock** instead of exiting
  when another daemon holds it. It would take over the moment the user
  stopped that daemon, so a stop would not stop the music's daemon.
