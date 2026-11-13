# ADR-0019: MPRIS and the system tray live in the daemon process

- **Status:** Accepted
- **Date:** 2026-02-05 (moved into the daemon 2026-07-05, tray 2026-07-07,
  volume writes queued 2026-09-25, Linux tray check 2026-11-09, tray
  state icons 2026-11-13)
- **Sources:** d0a22e0, 95fb723, d1d33df, be4000e, a6f0d6f; `internal/platform`, `internal/server/mpris.go`

## Context

Media keys and a tray icon only make sense if they work while the TUI is
closed, which after ADR-0001 means they must belong to the daemon.

## Decision

- MPRIS over D-Bus on Linux, a no-op stub elsewhere, using the
  `_linux.go` / `_other.go` build-tag pair. Shared message types live in
  `platform/command.go` rather than the per-OS files.
- The tray lives in the daemon and reuses the MPRIS command router, so its
  clicks map onto the existing playback methods. It owns the native GUI
  run loop: the daemon serves connections on a goroutine and blocks on the
  tray on the main goroutine. `--no-tray` and a headless check (CGSession
  on macOS, display/bus environment on Linux) skip it so the daemon runs
  anywhere.
  - *Amendment 2026-11-09.* On Linux the check is whether the session bus
    answers, not whether `DISPLAY`, `WAYLAND_DISPLAY` or
    `DBUS_SESSION_BUS_ADDRESS` is set. The tray there is a
    StatusNotifierItem, D-Bus and nothing else, so a display is neither
    needed nor enough: with one set and no bus to reach (an X-forwarded
    container, WSLg without systemd, `startx` without a user bus) the old
    check passed, the vendored systray started without a connection, and
    at shutdown it closed that nil connection, a panic on the main
    goroutine in the middle of `Shutdown`. `tray.Available` dials a
    private connection and closes it: the shared one (`dbus.SessionBus`)
    is the one systray takes and closes at quit. It does not start a bus
    (`dbus-launch`), which would have no tray host on it. `Tray.Run` also
    recovers a panic from the tray's loop and logs it, since systray's
    other failed starts leave the connection nil too; a tray is never
    worth the daemon going down over.
- Play-ish commands are dispatched off the D-Bus goroutine because they
  block on the network; Shutdown likewise, to avoid deadlocking the
  dispatcher. MPRIS `Quit` performs a real shutdown since `CanQuit` is
  advertised.
- MPRIS property-write callbacks never take the server lock synchronously
  (2026-09-25). godbus runs the `Volume` property's write callback while
  holding its property lock, and the server takes that lock (mirroring
  state to MPRIS) while holding its own, so a synchronous `SetVolume` there
  deadlocked the daemon. A volume write is handed to a single worker
  through a one-slot, latest-wins queue instead, so a dragged slider still
  ends on its last position. The worker mirrors the value it applies back
  to the property, so a client volume change landing between the write and
  the worker cannot leave MPRIS showing a volume the player is not at.
- The tray icon shows the playback state (2026-11-13): the full mark while
  playing, its arcs dimmed while stopped, and, while a stream connects or
  reconnects, a three-frame animation lighting the arcs outwards about
  three times a second. On macOS the icons are template images, so the
  states differ by opacity rather than colour and keep the menu bar's
  light/dark adaptation. The animation runs on its own ticker only in
  those states, because on Linux every frame is a D-Bus property change
  and a signal to the panel. Unlike MPRIS, which knows only playing and
  stopped, the tray is told every status: the server mirrors it from each
  state broadcast (`updateTrayLocked`), which the tray's per-item dedupe
  makes free when nothing it shows changed.
- Redundant tray Pause/Stop items were removed: `PlayPause` already tears
  down the stream when playing.

## Consequences

- macOS release builds compile with cgo per architecture before `lipo`.
- `mpris:artUrl` carries the channel image since ec842a7, threaded
  through the server-to-platform boundary.
