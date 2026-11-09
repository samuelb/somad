//go:build linux

package tray

import "github.com/godbus/dbus/v5"

// Available reports whether the session bus answers, which is what the
// Linux tray is made of: a StatusNotifierItem and its menu, both D-Bus. A
// display is neither needed for that nor enough, so DISPLAY and
// WAYLAND_DISPLAY are not consulted: with one set and no bus to reach (an
// X-forwarded container, WSLg without systemd, startx on a box without a
// user bus) the tray could never show. Without a bus it returns false, so
// the server runs without a tray. With one, the tray still degrades
// gracefully if no StatusNotifierHost is watching.
//
// The probe dials a connection of its own and closes it. godbus's shared
// connection (dbus.SessionBus) is the one systray takes and closes at quit,
// and closing a shared connection is not ours to do. It does not start a
// bus either (dbus-launch), unlike godbus's default: a bus nobody else is
// on has no tray host.
func Available() bool {
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	if err := conn.Auth(nil); err != nil {
		return false
	}
	return conn.Hello() == nil
}
