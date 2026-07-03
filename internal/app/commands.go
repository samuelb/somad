package app

import (
	"time"

	"somatui/internal/audio"
	"somatui/internal/channels"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	channelRefreshInterval = 10 * time.Minute
	trackUpdateInterval    = 2 * time.Second
)

// ChannelsLoadedMsg is a message sent when channels are successfully loaded.
type ChannelsLoadedMsg struct {
	Channels  *channels.Channels
	FromCache bool
}

// ChannelsRefreshedMsg is a message sent when channels are refreshed from network.
type ChannelsRefreshedMsg struct {
	Channels *channels.Channels
}

// ErrorMsg is a message sent when an error occurs.
type ErrorMsg struct {
	Err error
}

// TrackUpdateMsg is a message sent when track information is updated.
type TrackUpdateMsg struct {
	TrackInfo audio.TrackInfo
}

// TrackPollTickMsg is a message sent when it's time to poll for track updates.
type TrackPollTickMsg struct{}

// StreamErrorMsg is a message sent when a stream error occurs. ChannelID is
// set when the error belongs to a specific play request (so stale requests can
// be ignored) and empty for runtime errors on the active stream.
type StreamErrorMsg struct {
	Err       error
	ChannelID string
}

// PlaybackStartedMsg is sent when a play request has connected and audio is
// running.
type PlaybackStartedMsg struct {
	ChannelID string
}

// ChannelRefreshTickMsg is a message sent when it's time to refresh channels.
type ChannelRefreshTickMsg struct{}

// LoadChannels is a Tea command that fetches SomaFM channels asynchronously.
func LoadChannels(userAgent string) tea.Cmd {
	return func() tea.Msg {
		// Try cache first
		chans, err := channels.ReadChannelsFromCache()
		if err == nil {
			return ChannelsLoadedMsg{Channels: chans, FromCache: true}
		}

		// Fall back to network
		chans, err = channels.FetchChannelsFromNetwork(userAgent)
		if err != nil {
			return ErrorMsg{Err: err}
		}
		return ChannelsLoadedMsg{Channels: chans, FromCache: false}
	}
}

// RefreshChannels fetches channels from network in the background.
func RefreshChannels(userAgent string) tea.Msg {
	chans, err := channels.FetchChannelsFromNetwork(userAgent)
	if err != nil {
		// Silently ignore background refresh errors
		return nil
	}
	return ChannelsRefreshedMsg{Channels: chans}
}

// TickChannelRefresh returns a command that triggers a channel refresh periodically.
func TickChannelRefresh() tea.Cmd {
	return tea.Tick(channelRefreshInterval, func(t time.Time) tea.Msg {
		return ChannelRefreshTickMsg{}
	})
}

// PollTrackUpdates is a Tea command that polls the player for now-playing
// title updates, demuxed from the playback stream's ICY metadata. The channel
// is captured here so the tick callback (which runs on a timer goroutine)
// never touches the model.
func (m *Model) PollTrackUpdates() tea.Cmd {
	if m.Player == nil {
		return nil
	}
	updates := m.Player.TrackUpdates()
	return tea.Tick(trackUpdateInterval, func(t time.Time) tea.Msg {
		select {
		case trackInfo := <-updates:
			return TrackUpdateMsg{TrackInfo: trackInfo}
		default:
			return TrackPollTickMsg{}
		}
	})
}

// ListenStreamErrors waits for the next async stream error.
func (m *Model) ListenStreamErrors() tea.Cmd {
	return func() tea.Msg {
		if m.Player == nil {
			return nil
		}
		err, ok := <-m.Player.Errors()
		if !ok || err == nil {
			return nil
		}
		return StreamErrorMsg{Err: err}
	}
}

// UpdateMPRIS updates MPRIS metadata based on current playback state.
func (m *Model) UpdateMPRIS(items []list.Item) {
	if m.MPRIS == nil {
		return
	}
	ch := m.GetPlayingChannel(items)
	if ch == nil {
		m.MPRIS.SetStopped()
		return
	}
	track := ""
	if m.TrackInfo != nil {
		track = m.TrackInfo.Title
	}
	// Use channel title as artist since SomaFM streams don't have separate artist info
	m.MPRIS.SetPlaying(ch.Title, track, ch.Title)
}
