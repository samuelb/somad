# ADR-0037: A daemon whose audio device is lost stops and says so; a local client puts a new daemon in its place

- **Status:** Accepted (amended 2026-11-10: asked to play, the daemon restarts itself)
- **Date:** 2026-11-10
- **Sources:** the 2026-11 review; `internal/audio/player.go` (`ErrAudioDeviceLost`, ADR-0017's amendment of 2026-11-09), `internal/server/playback.go` (`noteDeviceLostLocked`, `beginPlay`, `failStreamLocked`), `internal/protocol/types.go` (`PlaybackState.DeviceLost`, `HelloResult.DeviceLost`), `internal/client/spawn.go`, `internal/app`; for the amendment `restartToPlayLocked` and `resumePlayback` in `internal/server/playback.go`, `restartSelf` in `cmd/soma/daemon.go`, `internal/state/resume.go`

## Context

oto opens one audio device context per process and never clears a fatal
error on it: once the sound server has gone away under it (PipeWire or
PulseAudio restarted, an ALSA write that cannot be recovered, a failed
AudioQueue start on macOS), or once it failed to initialize, the device
is gone until the process ends. The player reports that as
`audio.ErrAudioDeviceLost` (ADR-0017). The daemon treated it like any
audio-device error, which ADR-0013 retries forever: it fetched the
playlist and opened a stream about once a minute for as long as it ran,
each attempt failing at the device, and nothing told the user that only
`soma daemon stop` would help.

## Decision

- **The daemon gives up on that device, at once and for good.** On the
  first `ErrAudioDeviceLost`, from a play attempt or from the session
  playing, it ends whatever it was doing (a stream connecting, a
  reconnect scheduled), goes to stopped and schedules no reconnect. Every
  later play request is refused before a playlist is fetched. This is the
  one exception to "reconnecting never gives up" (ADR-0013): that rule is
  about streams, which come back, and the device does not.
  - *Amendment 2026-11-10.* A play request is still not played by this
    process, but it is no longer just refused: see "The daemon restarts
    itself when it is asked to play" below.
- **It says so in the state.** `PlaybackState.DeviceLost` is set, and
  `StreamError` carries the reason and "the soma daemon has to be
  restarted to play again" in every snapshot from then on, also after a
  stop, which otherwise clears the error. `HelloResult.DeviceLost` repeats
  the flag, so a command about to ask for playback knows without another
  round trip. Both fields are additions older clients ignore, so
  `protocol.Version` stays (ADR-0002).
- **A client that may restart the daemon replaces it before it asks for
  playback.** Only a new process gets a working device, and restarting
  the daemon is something a local client already does for version skew
  (ADR-0006), by the same means: `shutdown`, then a fresh spawn. A lost
  device counts like skew there, except that nothing is playing, so there
  is no music to protect and the restart need not wait for a command that
  interrupts playback. The CLI does it in `EnsureServer`, the TUI before
  the next play, with the channel asked for played once the new daemon is
  up. A remote daemon is never restarted (ADR-0006): its clients show the
  error, and the daemon has to be restarted where it runs.
  - *Amendment 2026-11-10.* A remote client still never restarts the
    daemon, but its play request now makes the daemon restart itself.
- **The daemon does not restart itself.** See the rejected alternatives.
  - *Amendment 2026-11-10.* Reversed for one case, the next bullet.
- **The daemon restarts itself when it is asked to play** (amendment
  2026-11-10). The tray, MPRIS and a remote client cannot replace the
  daemon as a local client does, so with them the music stayed off until
  someone ran a `soma` command on that machine. Now a play request that
  reaches a daemon without a device (`beginPlay`) makes it shut down and
  start again, and the new process plays what was asked for:
  - The old process leaves a note in the state directory, `resume.json`:
    the channel and the pending sleep timer. A file, because it is the
    one thing that reaches the new process both ways it can be started.
  - A supervised daemon exits with status 75 and its service manager
    starts it again (ADR-0038). Any other re-executes itself
    (`syscall.Exec` of its own executable with its own arguments), so it
    stays the same process, with the same flags, to whatever started it:
    a terminal, a service manager it was not told about, a client.
  - The new process takes the note once it holds the single-instance
    lock, removes it, and plays the channel. A note older than a minute
    is dropped, so a daemon started later for another reason never begins
    to play on its own.
  - The request that caused the restart is answered with an error that
    says so; the daemon waits 100 ms before it shuts down so that the
    answer gets out.
  - Only a request to play restarts it. Losing the device does not, nor
    does a failed resume, so a sound server that stays down cannot make
    the daemon restart in a loop: every restart answers a request.
  - A local client still replaces the daemon itself first (above). It
    gets its answer from the daemon that plays, where the request to the
    old one can only fail.

## Consequences

- After the sound server was restarted, the music stops with the reason
  shown, and the next `soma play` or channel picked in the TUI plays
  again, through a new daemon. Volume, favorites and the last channel
  carry over as in any restart; a pending sleep timer is carried by the
  client (ADR-0006).
- The tray and MPRIS cannot recover on their own: they live in the
  daemon, so a channel picked from the tray menu fails until a `soma`
  command or the TUI has replaced the daemon.
  - *Amendment 2026-11-10.* No longer: Play, a channel or Next from the
    tray or a media key restarts the daemon, and it plays about a second
    later. The tray icon goes and comes back with the process.
- While the daemon restarts itself (amendment 2026-11-10), a TUI
  connected to it loses the connection and reconnects. A local one may
  spawn a daemon in the gap; the single-instance lock lets one of the two
  run, and whichever it is finds the note and plays. If it is the spawned
  one, the daemon runs without the flags the old one was started with, as
  after any restart by a client (ADR-0006); a daemon whose flags matter
  runs supervised, where clients wait for the service manager instead
  (ADR-0038).
- A remote `soma play` that causes the restart reports the daemon's
  answer as an error, though the channel then plays.
- If the executable is gone when the daemon re-executes itself (removed
  by an upgrade), the daemon logs that and exits; the next local `soma`
  command starts one.
- A device that is merely not ready in time is not lost
  (`ErrAudioDevice` without `ErrAudioDeviceLost`) and is still retried.
- Code that adds a way to start playback must go through `beginPlay`, or
  repeat its check.

## Rejected alternatives

- **Keep retrying.** No attempt can succeed in the same process, and each
  one costs a playlist fetch and a stream connection.
- **The daemon replaces itself** (`exec` of its own binary, resuming the
  channel). It would heal without a client, tray and MPRIS included. But
  the new image has to take over the socket, the single-instance lock and
  a pending sleep timer, a client reconnecting in the gap would spawn a
  competing daemon, and a sound server that stays down needs a guard
  against restarting in a loop. That is a lot of machinery for a rare
  event, beside a restart path the clients already have and test. Worth
  another look if tray-only use turns out to matter.
  - *Amendment 2026-11-10.* Taken up, in the smaller form above. The
    lock and the socket are not handed over but released and taken
    again, which the lock already arbitrates. The sleep timer travels in
    the note. And the loop guard is that the daemon restarts only when
    asked to play.
- **Restart as soon as the device is lost, and resume what was playing**
  (2026-11-10). It would bring the music back with nobody asking. But at
  that moment the sound server is usually not back yet, so the new
  process would lose its device as well, and then needs a policy for
  when to try again and when to stop trying. Restarting on a request
  needs none.
- **Exit and let whoever comes next start a daemon.** The reason would be
  lost with the process: a TUI would reconnect to a fresh, silent daemon
  with nothing to say why the music stopped, and a tray-only session
  would lose its icon.
