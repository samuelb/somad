package protocol

import (
	"time"

	"somad/internal/channels"
)

// Playback status values for PlaybackState.Status.
const (
	StatusStopped      = "stopped"
	StatusConnecting   = "connecting"
	StatusPlaying      = "playing"
	StatusReconnecting = "reconnecting"
)

// PlaybackState is a full snapshot of the server's playback state. Every
// state event carries one, so clients can render from the latest snapshot
// alone without tracking deltas.
type PlaybackState struct {
	Status           string  `json:"status"`
	ChannelID        string  `json:"channelId,omitempty"`
	ChannelTitle     string  `json:"channelTitle,omitempty"`
	TrackTitle       string  `json:"trackTitle,omitempty"`
	Volume           float64 `json:"volume"`
	StreamError      string  `json:"streamError,omitempty"`
	ReconnectAttempt int     `json:"reconnectAttempt,omitempty"`
	// StopAt is when a pending sleep-timer stop (armed by StopParams.In) will
	// fire, as an RFC 3339 timestamp; empty when none is pending.
	StopAt string `json:"stopAt,omitempty"`
}

// ChannelsPayload carries the full channel catalog together with the
// persisted per-user data that affects how clients present it.
type ChannelsPayload struct {
	Channels      []channels.Channel `json:"channels"`
	Favorites     []string           `json:"favorites,omitempty"`
	LastChannelID string             `json:"lastChannelId,omitempty"`
	// Error is set when the catalog could not be loaded at all (no cache and
	// the network fetch failed); it clears on the next successful load.
	Error string `json:"error,omitempty"`
}

// HelloParams is the first request on every connection.
type HelloParams struct {
	ClientVersion   string `json:"clientVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// HelloResult identifies the server.
type HelloResult struct {
	ServerVersion   string `json:"serverVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
	PID             int    `json:"pid"`
}

// PlayParams selects the channel to play.
type PlayParams struct {
	ChannelID string `json:"channelId"`
}

// PlayRelativeParams selects a channel relative to the current (or last
// played) one in catalog order: +1 for next, -1 for previous.
type PlayRelativeParams struct {
	Delta int `json:"delta"`
}

// SetVolumeParams carries the target volume in [0, 1]; the server clamps.
type SetVolumeParams struct {
	Volume float64 `json:"volume"`
}

// StopParams is the optional payload of a stop request. The zero value
// (both fields empty) stops playback immediately, as before this type
// existed. In, when non-empty, is a Go duration string (time.ParseDuration,
// e.g. "45m"): instead of stopping now, the server arms a sleep timer that
// stops playback after that long, replacing any timer already pending; a
// play started before the timer fires keeps playing until it does. Cancel
// drops a pending sleep timer without stopping playback. In and Cancel are
// mutually exclusive.
type StopParams struct {
	In     string `json:"in,omitempty"`
	Cancel bool   `json:"cancel,omitempty"`
}

// ToggleFavoriteParams selects the channel whose favorite flag to flip.
type ToggleFavoriteParams struct {
	ChannelID string `json:"channelId"`
}

// FavoritesResult is the favorites list after a toggle.
type FavoritesResult struct {
	Favorites []string `json:"favorites"`
}

// HistoryParams requests recent now-playing history. ChannelID, when set,
// filters to one channel and allows the server to backfill from SomaFM's own
// song history for it; empty returns the most recent entries across every
// channel played this daemon lifetime. Limit bounds the number of entries
// returned; <= 0 uses the server's default.
type HistoryParams struct {
	ChannelID string `json:"channelId,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// HistoryEntry is one now-playing title change.
type HistoryEntry struct {
	ChannelID    string    `json:"channelId"`
	ChannelTitle string    `json:"channelTitle"`
	Title        string    `json:"title"`
	Time         time.Time `json:"time"`
}

// HistoryResult is the answer to a history request, newest entry first.
type HistoryResult struct {
	Entries []HistoryEntry `json:"entries"`
}
