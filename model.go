package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// model represents the application's state.
type model struct {
	list            list.Model
	player          *Player
	playing         int // Index of the playing channel, -1 if not playing
	status          string
	loading         bool
	err             error
	config          *Config
	trackInfo       *TrackInfo
	metadataReader  *MetadataReader
	trackUpdateChan chan TrackInfo
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

// Init initializes the application, loading channels asynchronously.
func (m *model) Init() tea.Cmd {
	return tea.Batch(loadChannels, tea.EnterAltScreen)
}

// Update handles incoming messages and updates the model's state.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			// Stop playback and quit the application
			if m.player != nil {
				m.player.Stop()
			}
			// Stop metadata reading
			if m.metadataReader != nil {
				m.metadataReader.Stop()
			}
			return m, tea.Quit
		case "enter", " ":
			// Play the selected channel
			if i, ok := m.list.SelectedItem().(item); ok {
				m.playing = m.list.Index()

				// Save the ID of the last selected channel to config
				if m.config != nil {
					m.config.LastSelectedChannelID = i.channel.ID
					if err := SaveConfig(m.config); err != nil {
						// Log the error but don't interrupt the user experience
						fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
					}
				}

				// Find the highest quality MP3 playlist URL
				var playlistURL string
				for _, playlist := range i.channel.Playlists {
					if playlist.Format == "mp3" {
						playlistURL = playlist.URL // This is a basic selection, could be improved
					}
				}

				if playlistURL != "" {
					// Get the actual stream URL from the playlist file
					streamURL, err := getStreamURLFromPlaylist(playlistURL)
					if err != nil {
						m.status = fmt.Sprintf("Error getting stream URL: %v", err)
					} else {
						// Start playing the audio stream
						err := m.player.Play(streamURL)
						if err != nil {
							m.status = fmt.Sprintf("Error playing: %v", err)
						} else {
							m.status = fmt.Sprintf("Playing: %s...", i.channel.Title)

							// Stop any existing metadata reader and start a new one
							if m.metadataReader != nil {
								m.metadataReader.Stop()
							}
							m.metadataReader = NewMetadataReader(streamURL)
							m.trackUpdateChan = make(chan TrackInfo, 1)
							m.metadataReader.Start()

							// Clear any existing track info to prevent showing outdated data
							m.trackInfo = nil

							// Start polling for track updates
							return m, m.pollTrackUpdates()
						}
					}
				} else {
					m.status = "No MP3 stream found for this channel."
				}
			}
		case "s":
			// Stop current playback
			if m.player != nil {
				m.player.Stop()
				m.playing = -1
				m.status = "Stopped"

				// Stop metadata reading
				if m.metadataReader != nil {
					m.metadataReader.Stop()
					m.metadataReader = nil
				}
				m.trackInfo = nil
			}
		}
	case tea.WindowSizeMsg:
		// Update the list's dimensions when the window size changes
		// Leave space for top margin, header, status line, and help
		m.list.SetSize(msg.Width, msg.Height-5)
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
		if m.config != nil && m.config.LastSelectedChannelID != "" {
			for i, ch := range msg.channels.Channels {
				if ch.ID == m.config.LastSelectedChannelID {
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
	listWidth := m.list.Width()
	listenerColWidth := 12
	leftColWidth := listWidth - listenerColWidth - 4

	if leftColWidth < 20 {
		leftColWidth = 20
	}

	// Title on the left
	title := titleStyle.
		Width(leftColWidth).
		Render("SomaFM Stations")

	// "Listeners" column header on the right
	listenerHeader := lipgloss.NewStyle().
		Foreground(subtleColor).
		Width(listenerColWidth).
		Align(lipgloss.Right).
		Render("Listeners")

	return lipgloss.JoinHorizontal(lipgloss.Bottom, title, listenerHeader)
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
		trackStr := "♪ " + m.trackInfo.Title
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
