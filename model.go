package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	listenerColumnWidth = 12
	minLeftColumnWidth  = 20
)

// model represents the application's state.
type model struct {
	list           list.Model
	player         *Player
	playing        int // Index of the playing channel, -1 if not playing
	status         string
	loading        bool
	err            error
	state          *State
	trackInfo      *TrackInfo
	metadataReader *MetadataReader
}

// channelsLoadedMsg is a message sent when channels are successfully loaded.
type channelsLoadedMsg struct {
	channels  *Channels
	fromCache bool
}

// channelsRefreshedMsg is a message sent when channels are refreshed from network.
type channelsRefreshedMsg struct {
	channels *Channels
}

// errorMsg is a message sent when an error occurs.
type errorMsg struct {
	err error
}

// trackUpdateMsg is a message sent when track information is updated.
type trackUpdateMsg struct {
	trackInfo TrackInfo
}

// streamErrorMsg is a message sent when a stream error occurs.
type streamErrorMsg struct {
	err error
}

// channelRefreshTickMsg is a message sent when it's time to refresh channels.
type channelRefreshTickMsg struct{}

// Init initializes the application, loading channels asynchronously.
func (m *model) Init() tea.Cmd {
	return tea.Batch(loadChannels, tea.EnterAltScreen, tickChannelRefresh())
}

// stopMetadataReader stops any active metadata reader.
func (m *model) stopMetadataReader() {
	if m.metadataReader != nil {
		m.metadataReader.Stop()
		m.metadataReader = nil
	}
}

// selectMP3PlaylistURL finds the first MP3 playlist URL from a channel's playlists.
func selectMP3PlaylistURL(playlists []Playlist) string {
	for _, playlist := range playlists {
		if playlist.Format == "mp3" {
			return playlist.URL
		}
	}
	return ""
}

// playChannel starts playing the given channel.
func (m *model) playChannel(i item) tea.Cmd {
	m.playing = m.list.Index()

	// Save the last selected channel
	if m.state != nil {
		m.state.LastSelectedChannelID = i.channel.ID
		if err := SaveState(m.state); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving state: %v\n", err)
		}
	}

	playlistURL := selectMP3PlaylistURL(i.channel.Playlists)
	if playlistURL == "" {
		m.status = "No MP3 stream found for this channel."
		return nil
	}

	streamURL, err := getStreamURLFromPlaylist(playlistURL)
	if err != nil {
		m.status = fmt.Sprintf("Error getting stream URL: %v", err)
		return nil
	}

	if err := m.player.Play(streamURL); err != nil {
		m.status = fmt.Sprintf("Error playing: %v", err)
		return nil
	}

	m.status = fmt.Sprintf("Playing: %s...", i.channel.Title)
	m.stopMetadataReader()
	m.metadataReader = NewMetadataReader(streamURL)
	m.metadataReader.Start()
	m.trackInfo = nil

	return m.pollTrackUpdates()
}

// Update handles incoming messages and updates the model's state.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.player != nil {
				m.player.Stop()
			}
			m.stopMetadataReader()
			return m, tea.Quit
		case "enter", " ":
			if i, ok := m.list.SelectedItem().(item); ok {
				return m, m.playChannel(i)
			}
		case "s":
			if m.player != nil {
				m.player.Stop()
				m.playing = -1
				m.status = "Stopped"
				m.stopMetadataReader()
				m.trackInfo = nil
			}
		}
	case tea.WindowSizeMsg:
		// Dynamically calculate the height needed for the header and status bar
		headerHeight := lipgloss.Height(m.renderHeader())
		statusBarHeight := lipgloss.Height(m.renderStatusBar())

		// Total height occupied by elements other than the list itself
		// Includes:
		// - 1 line for the top margin (the empty string in m.View())
		// - The calculated headerHeight
		// - The calculated statusBarHeight
		// - Plus 1 for safety/extra margin (adjust as needed)
		totalFixedUIHeight := 1 + headerHeight + statusBarHeight + 1

		// Update the list's dimensions when the window size changes
		m.list.SetSize(msg.Width, msg.Height-totalFixedUIHeight)
		return m, nil

	case channelsLoadedMsg:
		// Channels have been loaded, update the list and stop loading indicator
		items := make([]list.Item, len(msg.channels.Channels))
		for i, ch := range msg.channels.Channels {
			items[i] = item{channel: ch}
		}
		m.list.SetItems(items)
		m.loading = false

		// Set the cursor to the last selected channel if available
		if m.state != nil && m.state.LastSelectedChannelID != "" {
			for i, ch := range msg.channels.Channels {
				if ch.ID == m.state.LastSelectedChannelID {
					m.list.Select(i)
					break
				}
			}
		}

		// If loaded from cache, refresh from network in background
		if msg.fromCache {
			return m, refreshChannels
		}
	case channelsRefreshedMsg:
		// Channels have been refreshed from network, update the list
		selectedIndex := m.list.Index()
		items := make([]list.Item, len(msg.channels.Channels))
		for i, ch := range msg.channels.Channels {
			items[i] = item{channel: ch}
		}
		m.list.SetItems(items)

		// Restore selection position
		if selectedIndex < len(items) {
			m.list.Select(selectedIndex)
		}
	case channelRefreshTickMsg:
		// Time to refresh channels, fetch from network and schedule next tick
		return m, tea.Batch(refreshChannels, tickChannelRefresh())
	case errorMsg:
		// An error occurred during channel loading
		m.err = msg.err
		m.loading = false
	case trackUpdateMsg:
		// Track information has been updated
		m.trackInfo = &msg.trackInfo
	case streamErrorMsg:
		// Stream error occurred
		m.status = fmt.Sprintf("Stream error: %v", msg.err)
		m.playing = -1
	}

	// Update the list component and return its command
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// renderHeader renders the list header with column titles.
func (m *model) renderHeader() string {
	leftColWidth, listenerColWidth := calculateColumnWidths(m.list.Width())

	title := titleStyle.Width(leftColWidth).Render("SomaFM Stations")
	listenerHeader := lipgloss.NewStyle().
		Foreground(subtleColor).
		Width(listenerColWidth).
		Align(lipgloss.Right).
		Render("Listeners")

	return lipgloss.JoinHorizontal(lipgloss.Bottom, title, listenerHeader)
}

// calculateColumnWidths returns the left and listener column widths for a given total width.
func calculateColumnWidths(totalWidth int) (leftCol, listenerCol int) {
	listenerCol = listenerColumnWidth
	leftCol = totalWidth - listenerCol - 4
	if leftCol < minLeftColumnWidth {
		leftCol = minLeftColumnWidth
	}
	return
}

// renderStatusBar renders the styled status bar.
func (m *model) renderStatusBar() string {
	var icon, stateText string
	var stateStyle lipgloss.Style

	// Determine state and styling
	if m.playing < 0 {
		icon = "■"
		stateText = "Stopped"
		stateStyle = statusStoppedStyle
	} else {
		icon = "▶"
		stateText = "Playing"
		stateStyle = statusPlayingStyle
	}

	// Build the status line
	parts := []string{stateStyle.Render(icon + " " + stateText)}

	// Add channel name if playing
	if m.playing >= 0 {
		if items := m.list.Items(); m.playing < len(items) {
			if i, ok := items[m.playing].(item); ok {
				channelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
				parts = append(parts, channelStyle.Render(i.channel.Title))
			}
		}
	}

	// Add track info with music note
	if m.trackInfo != nil && m.trackInfo.Title != "" {
		trackStr := "♫ " + m.trackInfo.Title
		parts = append(parts, trackInfoStyle.Render(trackStr))
	}

	// Add error message if present
	if m.err != nil {
		parts = append(parts, statusErrorStyle.Render(m.err.Error()))
	}

	return statusBarStyle.Render(strings.Join(parts, "  │  "))
}

// View renders the application's UI.
func (m *model) View() string {
	// Display loading message if channels are still being fetched
	if m.loading {
		return loadingStyle.Render("◌ Loading SomaFM channels...")
	}

	// Display error message if an error occurred
	if m.err != nil {
		errorContent := fmt.Sprintf("✕ Error loading channels\n\n%v\n\nPress 'q' to quit or 'r' to retry", m.err)
		return errorBoxStyle.Render(errorContent)
	}

	// Build the view using lipgloss layout
	return lipgloss.JoinVertical(
		lipgloss.Left,
		"", // Top margin
		m.renderHeader(),
		m.list.View(),
		m.renderStatusBar(),
	)
}
