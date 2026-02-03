package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	listenerColumnWidth = 12
	minLeftColumnWidth  = 20
)

// aboutInfo holds version and metadata for the about screen.
type aboutInfo struct {
	Version string
	Commit  string
	Date    string
}

// model represents the application's state.
type model struct {
	list           list.Model
	player         *Player
	playing        int // Index of the playing channel, -1 if not playing
	loading        bool
	err            error
	state          *State
	trackInfo      *TrackInfo
	metadataReader *MetadataReader
	showAbout      bool
	about          aboutInfo
	width          int
	height         int
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
type streamErrorMsg struct{}

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
		return nil
	}

	streamURL, err := getStreamURLFromPlaylist(playlistURL)
	if err != nil {
		return nil
	}

	if err := m.player.Play(streamURL); err != nil {
		return nil
	}
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
		// Handle about screen dismissal
		if m.showAbout {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			default:
				m.showAbout = false
				return m, nil
			}
		}

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
				m.stopMetadataReader()
				m.trackInfo = nil
			}
		case "a":
			m.showAbout = true
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

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

	return statusBarStyle.Render(strings.Join(parts, "  │  "))
}

// placeOverlay places the foreground string on top of the background string
// at the specified x, y position.
func placeOverlay(x, y int, fg, bg string) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	for i, fgLine := range fgLines {
		bgLineIdx := y + i
		if bgLineIdx < 0 || bgLineIdx >= len(bgLines) {
			continue
		}

		bgLine := bgLines[bgLineIdx]
		bgLineWidth := ansi.StringWidth(bgLine)

		// Pad background line if needed
		if bgLineWidth < x {
			bgLine += strings.Repeat(" ", x-bgLineWidth)
			bgLineWidth = x
		}

		// Build the new line: left part + foreground + right part
		fgWidth := ansi.StringWidth(fgLine)
		leftPart := ansi.Truncate(bgLine, x, "")
		rightStart := x + fgWidth
		var rightPart string
		if rightStart < bgLineWidth {
			rightPart = ansi.TruncateLeft(bgLine, bgLineWidth-rightStart, "")
		}

		bgLines[bgLineIdx] = leftPart + fgLine + rightPart
	}

	return strings.Join(bgLines, "\n")
}

// renderAboutScreen renders the about dialog.
func (m *model) renderAboutScreen() string {
	content := fmt.Sprintf(`SomaUI

A terminal UI for SomaFM internet radio.

Version:  %s
Commit:   %s
Built:    %s

License:  MIT
Author:   Samuel Barabas
GitHub:   https://github.com/samuelb/somatui

This project is not affiliated with SomaFM.
All content and station streams are provided by somafm.com.

Press any key to close`, m.about.Version, m.about.Commit, m.about.Date)

	return aboutBoxStyle.Render(content)
}

// View renders the application's UI.
func (m *model) View() string {
	// Display loading message if channels are still being fetched
	if m.loading {
		return loadingStyle.Render("◌ Loading SomaFM channels...")
	}

	// Display error message if channel loading failed
	if m.err != nil {
		errorContent := fmt.Sprintf("✕ Error loading channels\n\n%v\n\nPress 'q' to quit", m.err)
		return errorBoxStyle.Render(errorContent)
	}

	// Build the main view using lipgloss layout
	mainView := lipgloss.JoinVertical(
		lipgloss.Left,
		"", // Top margin
		m.renderHeader(),
		m.list.View(),
		m.renderStatusBar(),
	)

	// Overlay about screen if requested
	if m.showAbout {
		aboutBox := m.renderAboutScreen()
		// Calculate position to center the about box
		aboutWidth := lipgloss.Width(aboutBox)
		aboutHeight := lipgloss.Height(aboutBox)
		x := (m.width - aboutWidth) / 2
		y := (m.height - aboutHeight) / 2
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		return placeOverlay(x, y, aboutBox, mainView)
	}

	return mainView
}
