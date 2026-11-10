# ADR-0037: A daemon whose audio device is lost stops and says so; a local client puts a new daemon in its place

- **Status:** Accepted
- **Date:** 2026-11-10
- **Sources:** the 2026-11 review; `internal/audio/player.go` (`ErrAudioDeviceLost`, ADR-0017's amendment of 2026-11-09), `internal/server/playback.go` (`noteDeviceLostLocked`, `beginPlay`, `failStreamLocked`), `internal/protocol/types.go` (`PlaybackState.DeviceLost`, `HelloResult.DeviceLost`), `internal/client/spawn.go`, `internal/app`

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
- **The daemon does not restart itself.** See the rejected alternatives.

## Consequences

- After the sound server was restarted, the music stops with the reason
  shown, and the next `soma play` or channel picked in the TUI plays
  again, through a new daemon. Volume, favorites and the last channel
  carry over as in any restart; a pending sleep timer is carried by the
  client (ADR-0006).
- The tray and MPRIS cannot recover on their own: they live in the
  daemon, so a channel picked from the tray menu fails until a `soma`
  command or the TUI has replaced the daemon.
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
- **Exit and let whoever comes next start a daemon.** The reason would be
  lost with the process: a TUI would reconnect to a fresh, silent daemon
  with nothing to say why the music stopped, and a tray-only session
  would lose its icon.
