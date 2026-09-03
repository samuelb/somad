// Package platform holds the desktop integrations (MPRIS on Linux, the tray,
// desktop notifications). This file carries everything the platform-tagged
// files share: the command sender, the messages they send, and string
// sanitising. Keeping them here, untagged, is what stops the _linux/_other
// pairs drifting apart.
package platform

import (
	"strings"
	"unicode/utf8"
)

// CmdSender is an interface for sending commands to the application.
// This matches the tea.Program's Send method signature (tea.Msg is any).
type CmdSender interface {
	Send(msg any)
}

// MPRISPlayMsg is sent when MPRIS requests to play.
type MPRISPlayMsg struct{}

// MPRISStopMsg is sent when MPRIS requests to stop.
type MPRISStopMsg struct{}

// MPRISPlayPauseMsg is sent when MPRIS requests to toggle play/pause.
type MPRISPlayPauseMsg struct{}

// MPRISNextMsg is sent when MPRIS requests to go to next track.
type MPRISNextMsg struct{}

// MPRISPrevMsg is sent when MPRIS requests to go to previous track.
type MPRISPrevMsg struct{}

// MPRISVolumeMsg is sent when MPRIS requests a volume change.
type MPRISVolumeMsg struct {
	Volume float64
}

// MPRISQuitMsg is sent when MPRIS requests the player to quit.
type MPRISQuitMsg struct{}

// PlayChannelMsg requests playback of a specific channel by ID. It is sent by
// the tray's channel picker and routed through the same command sender as the
// MPRIS messages.
type PlayChannelMsg struct {
	ID string
}

// ToggleFavoriteMsg requests flipping a channel's favorite flag. It is sent by
// the tray's Favorites submenu and routed through the same command sender as
// the MPRIS messages.
type ToggleFavoriteMsg struct {
	ID string
}

// SanitizeUTF8 removes invalid UTF-8 sequences from a string. D-Bus (MPRIS,
// notifications) requires valid UTF-8, and the tray applies it on every
// platform so menu titles never carry stray bytes from ICY metadata.
func SanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r != utf8.RuneError {
			b.WriteRune(r)
		}
	}
	return b.String()
}
