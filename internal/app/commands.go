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

// StreamErrorMsg is a message sent when a stream error occurs.
type StreamErrorMsg struct{}

// ChannelRefreshTickMsg is a message sent when it's time to refresh channels.
type ChannelRefreshTickMsg struct{}

// LoadChannels is a Tea command that fetches SomaFM channels asynchronously.
func LoadChannels() tea.Msg {
	// Try cache first
	chans, err := channels.ReadChannelsFromCache()
	if err == nil {
		return ChannelsLoadedMsg{Channels: chans, FromCache: true}
	}

	// Fall back to network
	chans, err = channels.FetchChannelsFromNetwork("SomaTUI")
	if err != nil {
		return ErrorMsg{Err: err}
	}
	return ChannelsLoadedMsg{Channels: chans, FromCache: false}
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

// PollTrackUpdates is a Tea command that polls for track information updates.
func (m *Model) PollTrackUpdates() tea.Cmd {
	return tea.Tick(trackUpdateInterval, func(t time.Time) tea.Msg {
		if m.MetadataReader == nil {
			return nil
		}

		select {
		case trackInfo := <-m.MetadataReader.GetUpdateChan():
			return TrackUpdateMsg{TrackInfo: trackInfo}
		default:
			return TrackPollTickMsg{}
		}
	})
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
