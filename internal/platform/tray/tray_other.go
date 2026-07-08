//go:build !darwin && !linux

// Package tray has no tray support on this platform; New returns nil and the
// server runs without a menu-bar item.
package tray

import "somad/internal/platform"

// Supported reports whether this build has a real tray implementation.
const Supported = false

// Available reports whether a GUI is present for the tray. Always false here:
// this platform has no tray implementation.
func Available() bool { return false }

// Channel is a catalog entry the tray offers for playback.
type Channel struct {
	ID       string
	Title    string
	Favorite bool
}

// Tray is an unused placeholder so callers still type-check on this platform.
type Tray struct{}

// New returns nil: there is no tray on this platform. Callers treat a nil Tray
// as "disabled".
func New() *Tray { return nil }

// SetSender is a no-op.
func (t *Tray) SetSender(s platform.CmdSender) {}

// SetOnQuit is a no-op.
func (t *Tray) SetOnQuit(fn func()) {}

// Run is a no-op.
func (t *Tray) Run(onReady func()) {}

// Quit is a no-op.
func (t *Tray) Quit() {}

// SetPlaying is a no-op.
func (t *Tray) SetPlaying(channelID, station, track string) {}

// SetStopped is a no-op.
func (t *Tray) SetStopped() {}

// SetChannels is a no-op.
func (t *Tray) SetChannels(channels []Channel) {}
