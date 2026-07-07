package platform

// PlayChannelMsg requests playback of a specific channel by ID. It is sent by
// the tray's channel picker and routed through the same command sender as the
// MPRIS messages. Unlike those, it is platform-independent, so it lives here
// rather than in the per-OS MPRIS files.
type PlayChannelMsg struct {
	ID string
}

// ToggleFavoriteMsg requests flipping a channel's favorite flag. It is sent by
// the tray's Favorites submenu and routed through the same command sender as
// the MPRIS messages.
type ToggleFavoriteMsg struct {
	ID string
}
