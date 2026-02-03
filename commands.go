package main

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// loadChannels is a Tea command that fetches SomaFM channels asynchronously.
func loadChannels() tea.Msg {
	// Try cache first
	channels, err := readChannelsFromCache()
	if err == nil {
		return channelsLoadedMsg{channels: channels, fromCache: true}
	}

	// Fall back to network
	channels, err = fetchChannelsFromNetwork()
	if err != nil {
		return errorMsg{err}
	}
	return channelsLoadedMsg{channels: channels, fromCache: false}
}

// refreshChannels fetches channels from network in the background.
func refreshChannels() tea.Msg {
	channels, err := fetchChannelsFromNetwork()
	if err != nil {
		// Silently ignore background refresh errors
		return nil
	}
	return channelsRefreshedMsg{channels: channels}
}

// tickChannelRefresh returns a command that triggers a channel refresh after 10 minutes.
func tickChannelRefresh() tea.Cmd {
	return tea.Tick(10*time.Minute, func(t time.Time) tea.Msg {
		return channelRefreshTickMsg{}
	})
}

// pollTrackUpdates is a Tea command that polls for track information updates.
func (m *model) pollTrackUpdates() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		if m.metadataReader == nil {
			return nil
		}

		select {
		case trackInfo := <-m.metadataReader.GetUpdateChan():
			return trackUpdateMsg{trackInfo: trackInfo}
		default:
			return nil
		}
	})
}
