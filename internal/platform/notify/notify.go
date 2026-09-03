// Package notify shows a desktop notification when the playing track
// changes (see server.notify / --notify; docs/adr/0030). It mirrors
// internal/platform's MPRIS split: D-Bus (org.freedesktop.Notifications) on
// Linux, `osascript` on macOS, and a no-op stub elsewhere — one Notifier
// type per build-tagged file, each with a New() constructor and a
// Notify(title, body string) method. A Notifier never fails loudly: errors
// are logged once and otherwise swallowed.
package notify
