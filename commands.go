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

// pollBufferUpdates is a Tea command that polls for buffer state updates.
func (m *model) pollBufferUpdates() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		if m.bufferStateChan == nil {
			return tickMsg{} // Keep UI refreshing
		}

		select {
		case stats, ok := <-m.bufferStateChan:
			if !ok {
				// Channel closed
				return tickMsg{}
			}
			return bufferUpdateMsg{stats: stats}
		default:
			return tickMsg{} // Keep UI refreshing even without new stats
		}
	})
}
