//go:build darwin

package tray

import "somad/internal/platform"

// Available reports whether a GUI session is present. It is false on a headless
// Mac (no console login), where starting the tray would attach AppKit to a
// missing window server and could hang or abort — so the server runs without
// a tray instead.
func Available() bool {
	return platform.HasGUISession()
}
