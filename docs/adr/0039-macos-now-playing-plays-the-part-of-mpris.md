# ADR-0039: On macOS the daemon registers with Now Playing, which does there what MPRIS does on Linux

- **Status:** Accepted
- **Date:** 2026-11-10
- **Sources:** the 2026-11 review; `internal/platform/mpris_darwin.go`, `internal/platform/nowplaying_darwin.go`, `serve` and `newMPRIS` in `cmd/soma/daemon.go`

## Context

On Linux the daemon is an MPRIS player (ADR-0019), so the media keys and
the desktop's media controls work with the TUI closed. macOS had the tray
only: the play/pause, next and previous keys went to whatever other app
had played last, and nothing showed what soma played. The system's
counterpart is the MediaPlayer framework: `MPNowPlayingInfoCenter` for
the entry in Control Center and on the lock screen, and
`MPRemoteCommandCenter` for the keys, headset buttons and the controls
shown there. The macOS build links system frameworks already, for AAC
(ADR-0016) and the tray (ADR-0019).

Tried first with a probe outside the tree: a plain command-line binary
with no bundle becomes the Now Playing app, and the system routes a
play/pause key to it, provided its main thread runs a run loop. Without
one it is never made the Now Playing app at all.

## Decision

- **`platform.MPRIS` on macOS is Now Playing.** It has the same methods as
  on Linux, so the server drives both through one type and one command
  router (`mprisSender`), and nothing in `internal/server` changed. The
  Objective-C is in the cgo preamble of `nowplaying_darwin.go`.
- **What is shown:** the track as title, the artist, and the station as
  album, marked as a live stream. Without a track title the station is the
  title.
- **The commands:** play, play/pause, next and previous map onto the MPRIS
  messages of the same meaning; pause and stop both stop the stream, as
  the MPRIS `Pause` method does, since live radio cannot hold its place.
- **The daemon becomes the Now Playing app with its first play, not at
  start.** An idle daemon must not take the media keys from the app the
  user plays with. From its first play on it stays registered.
- **Stopped is shown as paused.** macOS sends the play key to the app that
  played last for as long as that app does not report itself stopped, so
  a stop keeps the last station in Now Playing, paused, and the play key
  brings it back. Another app that plays takes the keys, as between any
  two players.
- **The main thread's run loop is the tray's, or one of its own.** With
  the tray (the default) the tray's `NSApplication` loop delivers the
  commands. With `--no-tray` in a GUI session, `serve` runs a bare
  `CFRunLoop` on the main thread in the tray's place
  (`MPRIS.RunMainLoop`). Every call into MediaPlayer is made in a block
  on the main queue.
- **No GUI session, no Now Playing.** On a headless Mac or over SSH
  `NewMPRIS` returns nil, by the check the tray uses
  (`platform.HasGUISession`).
- **Tests keep out of the real thing.** The daemon gets its media controls
  through `newMPRIS`, which the tests that run the real daemon replace
  with none, on Linux too: a test run must not show up in the media
  controls of whoever runs it or take their keys. `mpris_darwin_test.go`
  drives the Go side through a fake `nowPlayingCenter`.

## Consequences

- With the daemon playing or paused, the media keys, a headset's button,
  Control Center and the lock screen control it, tray or no tray.
- The entry carries no app name: the binary has no bundle, and the
  system's log shows it with a code signing identifier and no display
  name. What Control Center draws for it has not been looked at.
- `--no-tray` no longer means that nothing of the daemon appears on the
  desktop; there is no flag to turn Now Playing off, as there is none for
  MPRIS.
- A remote command is handled on the main thread, which the tray draws
  on. It must hand over to a goroutine at once (`MPRIS.command`), or a
  stop, which updates the tray while it holds the server's lock, could
  block against it.
- The Objective-C must stay out of a `.m` file: cgo links libobjc again
  for every package that has one, the vendored systray has one, and the
  linker then warns about the duplicate on every macOS build.
- Checked by hand against the real system (macOS 27.2, 2026-11-10), since
  no automated test can press a key: a daemon in a sandbox, playing at
  volume 0, was made the Now Playing app, and synthetic play/pause, next
  and previous keys stopped, resumed and switched it, both with the tray
  and with `--no-tray`. An idle daemon was not registered.

## Rejected alternatives

- **Channel artwork in Now Playing.** MPRIS takes the artwork as a URL
  the desktop fetches; Now Playing takes an image, so the daemon would
  have to fetch and decode it itself, through the outbound allowlist
  (ADR-0010). Left out of this step; listed in `TODO.md`.
- **Register at daemon start**, as on Linux, where the daemon is on the
  bus whether it plays or not. There a controller picks a player; here
  the keys go to one app, and a daemon that only sits in the background
  would take them from the music app the user had just paused.
- **A media-key event tap** (`CGEventTap` on the system-defined key
  events), which is how players did this before `MPRemoteCommandCenter`.
  It needs the Accessibility permission, takes the keys from every other
  app, and shows nothing in Now Playing.
- **A helper app bundle** to give the entry a name and an icon. A second
  artifact to build, sign and ship (ADR-0029) for a label.
