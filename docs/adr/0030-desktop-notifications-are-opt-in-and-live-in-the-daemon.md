# ADR-0030: Desktop notifications are opt-in and live in the daemon

- **Status:** Accepted
- **Date:** 2026-09-03
- **Sources:** TODO.md "Desktop notification on track change"; `internal/server/playback.go`, `internal/server/notify.go`, `internal/platform/notify`

## Context

TODO.md asked for a desktop notification on track change, showing the title
and artist, opt-in. Like MPRIS and the tray (ADR-0019), it only makes sense
fired from a process guaranteed to be running when a title changes — the
daemon, not a frontend. The raw ICY `StreamTitle` needed splitting into
artist/title first so the notification body could show a real artist
instead of repeating the channel name; that split (`audio.SplitTitle`) also
now feeds MPRIS's `xesam:artist`, landing in the same commit series.

## Decision

- The daemon fires the notification, from `handleTrackUpdate`, the same
  place that already updates MPRIS and broadcasts the state snapshot.
- A new `server.notify` config key / `--notify` daemon flag (default
  `false`) gates it: a popup on every song change is not for everyone, so
  it ships opt-in rather than on by default.
- `internal/platform/notify` mirrors `internal/platform`'s MPRIS build-tag
  split (`_linux.go` / `_darwin.go` / `_other.go`, one `Notifier` type per
  file): D-Bus (`org.freedesktop.Notifications.Notify`) on Linux,
  `osascript -e 'display notification ...'` on macOS, a no-op stub
  elsewhere. `osascript` needs no signed, bundled `.app` or entitlement the
  way `UNUserNotificationCenter` would, so it works from the plain binary
  this project ships today.
- Untrusted values (an ICY artist or title can contain anything) go into
  the AppleScript command as escaped string literals
  (`appleScriptQuote`), never through a shell, so there is no injection
  path through the notification text.
  - *Amendment 2026-11-09.* Linux had such a path after all. A
    notification server with the `body-markup` capability, as the common
    desktop ones have, parses the body as markup: `<`, `>` and `&` in an
    artist or track name lost text, and a title from the stream could
    carry the `<a href>` and `<img>` tags some servers honor. The
    notifier asks `GetCapabilities` once and, for a server with
    `body-markup`, sends the body with those three characters as
    `&lt;`, `&gt;` and `&amp;`; a server without it gets the body as it
    is, since it would show the entities as written. Only these three,
    not all of `html.EscapeString`: quotes need no escaping outside a
    tag. The summary is plain text to every server and is never escaped.
    Until `GetCapabilities` has answered nothing is sent, and the next
    title asks again.
- A small `notifyPipeline` (`internal/server/notify.go`) coalesces bursts:
  while a send is in flight (blocking on D-Bus, or forking `osascript`), a
  newer title just replaces the not-yet-sent one instead of queuing another
  goroutine, so a fast run of ICY titles — or a slow/stuck notification
  backend — cannot pile up work or stack notifications. On Linux the
  notification server's returned id is reused as `replaces_id`, so the
  visible bubble is replaced in place; macOS's Notification Center has no
  equivalent primitive, so each send there shows a fresh notification.
- `server.Config.Notifier` is an injectable `func(title, body string)`,
  defaulting to `notify.New().Notify`, so server tests exercise the whole
  hook — including the artist/title split and the coalescing pipeline —
  without touching D-Bus or forking `osascript`.
- Failures (no session bus, no notification daemon, `osascript` missing or
  denied permission, ...) are logged once per `Notifier` and otherwise
  swallowed: a notification is a nice-to-have, never worth the daemon
  going fatal over.

## Consequences

- `PlaybackState.TrackTitle` stays the raw, unsplit ICY string on the wire;
  only the daemon-local MPRIS and notification paths use the split
  artist/title, so this needed no protocol version bump.
- Every title change notifies when enabled, even a channel with no usable
  "Artist - Title" split — the body then falls back to just the channel
  name instead of "artist · channel".
- The Last.fm scrobbler (ADR-0031) reuses `audio.SplitTitle` the same
  way instead of re-deriving artist/title itself.

## Rejected alternatives

- A frontend-side notification (TUI only) was rejected outright: it would
  not fire with the TUI closed, defeating the point, for the same reason
  ADR-0019 put MPRIS and the tray in the daemon rather than the TUI.
