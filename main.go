package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Color palette - SomaFM inspired
var (
	titleColor     = lipgloss.Color("#ff0709") // Red for title
	primaryColor   = lipgloss.Color("#D8A24D") // Golden accent
	playingColor   = lipgloss.Color("#1a9096") // Teal for playing
	bufferingColor = lipgloss.Color("#D8A24D") // Golden for buffering
	errorColor     = lipgloss.Color("#FF3333") // Red for errors
	subtleColor    = lipgloss.Color("#666666") // Gray for secondary text
	dimColor       = lipgloss.Color("#444444") // Dim gray for backgrounds
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(titleColor).
			MarginLeft(2)

	statusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			MarginTop(1)

	statusPlayingStyle = lipgloss.NewStyle().
				Foreground(playingColor).
				Bold(true)

	statusBufferingStyle = lipgloss.NewStyle().
				Foreground(bufferingColor).
				Bold(true)

	statusStoppedStyle = lipgloss.NewStyle().
				Foreground(subtleColor)

	statusErrorStyle = lipgloss.NewStyle().
				Foreground(errorColor).
				Bold(true)

	trackInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CCCCCC")).
			Italic(true)

	loadingStyle = lipgloss.NewStyle().
			Foreground(primaryColor).
			Bold(true).
			Padding(2, 4)

	errorBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(errorColor).
			Foreground(errorColor).
			Padding(1, 2).
			MarginTop(2).
			MarginLeft(2)
)

// item implements the list.Item interface for displaying channels.
type item struct {
	channel Channel
}

// Title returns the title of the channel for display in the list.
func (i item) Title() string {
	return i.channel.Title
}

// Description returns the description of the channel for display in the list.
func (i item) Description() string { return i.channel.Description }

// FilterValue returns the title of the channel for filtering purposes.
func (i item) FilterValue() string { return i.channel.Title }

// Listeners returns the listener count for display.
func (i item) Listeners() string { return i.channel.Listeners }

// styledDelegate is a custom delegate for styling list items.
type styledDelegate struct {
	list.DefaultDelegate
	playingIndex *int
}

// newStyledDelegate creates a styled delegate for the list.
func newStyledDelegate(playingIndex *int) styledDelegate {
	d := list.NewDefaultDelegate()

	// Normal item styles
	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(subtleColor).
		Padding(0, 0, 0, 2)

	// Selected item styles
	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(primaryColor).
		Foreground(primaryColor).
		Bold(true).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(primaryColor).
		Foreground(lipgloss.Color("#CCCCCC")).
		Padding(0, 0, 0, 1)

	return styledDelegate{DefaultDelegate: d, playingIndex: playingIndex}
}

// Render renders a list item with custom styling, including a playing indicator.
func (d styledDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	// Check if this item is currently playing
	isPlaying := d.playingIndex != nil && *d.playingIndex == index
	isSelected := index == m.Index()

	// Build title with playing indicator
	title := i.Title()
	if isPlaying {
		title = "▶ " + title
	}

	// Calculate column widths
	listWidth := m.Width()
	listenerColWidth := 12                           // Space for "XXX listeners"
	leftColWidth := listWidth - listenerColWidth - 4 // 4 for padding/margins

	if leftColWidth < 20 {
		leftColWidth = 20
	}

	// Listener count styles
	listenerStyle := lipgloss.NewStyle().
		Foreground(subtleColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerSelectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CCCCCC")).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerPlayingStyle := lipgloss.NewStyle().
		Foreground(playingColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	// Apply styles based on state
	var titleStr, descStr, listenerStr string
	listeners := i.Listeners() + " ♪"

	if isSelected {
		titleStr = d.Styles.SelectedTitle.Copy().Width(leftColWidth).Render(title)
		descStr = d.Styles.SelectedDesc.Copy().Width(leftColWidth).Render(i.Description())
		listenerStr = listenerSelectedStyle.Render(listeners)
	} else if isPlaying {
		// Playing but not selected - show green indicator
		playingTitleStyle := lipgloss.NewStyle().
			Foreground(playingColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		playingDescStyle := lipgloss.NewStyle().
			Foreground(subtleColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		titleStr = playingTitleStyle.Render(title)
		descStr = playingDescStyle.Render(i.Description())
		listenerStr = listenerPlayingStyle.Render(listeners)
	} else {
		titleStr = d.Styles.NormalTitle.Copy().Width(leftColWidth).Render(title)
		descStr = d.Styles.NormalDesc.Copy().Width(leftColWidth).Render(i.Description())
		listenerStr = listenerStyle.Render(listeners)
	}

	// Build two-column layout
	// Title row with listener count
	titleRow := lipgloss.JoinHorizontal(lipgloss.Top, titleStr, listenerStr)
	// Description row (no listener count, just padding to align)
	descRow := descStr

	fmt.Fprintf(w, "%s\n%s", titleRow, descRow)
}

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
	bufferStats     *BufferStats
	bufferStateChan <-chan BufferStats
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

// bufferUpdateMsg is a message sent when buffer state changes.
type bufferUpdateMsg struct {
	stats BufferStats
}

// streamErrorMsg is a message sent when a stream error occurs.
type streamErrorMsg struct {
	err error
}

// tickMsg is a message sent to keep the UI refreshing.
type tickMsg struct{}

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
						// Start playing the audio stream (now returns buffer state channel)
						bufferChan, err := m.player.Play(streamURL)
						if err != nil {
							m.status = fmt.Sprintf("Error playing: %v", err)
						} else {
							m.status = fmt.Sprintf("Buffering: %s...", i.channel.Title)
							m.bufferStateChan = bufferChan
							m.bufferStats = nil

							// Stop any existing metadata reader and start a new one
							if m.metadataReader != nil {
								m.metadataReader.Stop()
							}
							m.metadataReader = NewMetadataReader(streamURL)
							m.trackUpdateChan = make(chan TrackInfo, 1)
							m.metadataReader.Start()

							// Clear any existing track info to prevent showing outdated data
							m.trackInfo = nil

							// Start polling for track and buffer updates
							return m, tea.Batch(m.pollTrackUpdates(), m.pollBufferUpdates())
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
				m.bufferStats = nil
				m.bufferStateChan = nil
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
		// Continue polling for updates
		return m, m.pollTrackUpdates()
	case bufferUpdateMsg:
		// Buffer state has been updated
		m.bufferStats = &msg.stats

		// Update status based on buffer state
		if m.playing >= 0 {
			if i, ok := m.list.SelectedItem().(item); ok {
				switch msg.stats.State {
				case BufferStateBuffering:
					m.status = fmt.Sprintf("Buffering: %s... %d%%", i.channel.Title, int(msg.stats.FillLevel*100))
				case BufferStateHealthy:
					m.status = fmt.Sprintf("Playing: %s", i.channel.Title)
				case BufferStateUnderrun:
					m.status = fmt.Sprintf("Rebuffering: %s... %d%%", i.channel.Title, int(msg.stats.FillLevel*100))
				case BufferStateError:
					m.status = fmt.Sprintf("Error: %v", msg.stats.LastError)
				case BufferStateClosed:
					m.status = "Stream closed"
				}
			}
		}

		// Continue polling for buffer updates
		return m, m.pollBufferUpdates()
	case tickMsg:
		// Continue polling to keep UI refreshing
		if m.playing >= 0 {
			return m, m.pollBufferUpdates()
		}
	case streamErrorMsg:
		// Stream error occurred
		m.status = fmt.Sprintf("Stream error: %v", msg.err)
		m.playing = -1
		m.bufferStats = nil
		m.bufferStateChan = nil
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
	title := titleStyle.Copy().
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
	} else if m.bufferStats != nil {
		switch m.bufferStats.State {
		case BufferStateBuffering, BufferStateUnderrun:
			icon = "◌"
			stateText = fmt.Sprintf("Buffering (%d%%)", int(m.bufferStats.FillLevel*100))
			stateStyle = statusBufferingStyle
		case BufferStateHealthy:
			icon = "▶"
			stateText = "Playing"
			stateStyle = statusPlayingStyle
		case BufferStateError:
			icon = "✕"
			stateText = "Error"
			stateStyle = statusErrorStyle
		case BufferStateClosed:
			icon = "■"
			stateText = "Closed"
			stateStyle = statusStoppedStyle
		default:
			icon = "◌"
			stateText = fmt.Sprintf("Buffering (%d%%)", int(m.bufferStats.FillLevel*100))
			stateStyle = statusBufferingStyle
		}
	} else if m.playing >= 0 {
		icon = "◌"
		stateText = "Connecting"
		stateStyle = statusBufferingStyle
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
	if m.bufferStats != nil && m.bufferStats.State == BufferStateError && m.bufferStats.LastError != nil {
		parts = append(parts, statusErrorStyle.Render(m.bufferStats.LastError.Error()))
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

func main() {
	// Initialize the audio player
	player, err := NewPlayer()
	if err != nil {
		fmt.Printf("Alas, there's been an error initializing the player: %v\n", err)
		os.Exit(1)
	}

	// Load application configuration
	config, err := LoadConfig()
	if err != nil {
		fmt.Printf("Alas, there's been an error loading config: %v\n", err)
		os.Exit(1)
	}

	// Create the main application model (need playing index for delegate)
	m := &model{
		player:          player,
		playing:         -1,
		loading:         true,
		config:          config,
		trackInfo:       nil,
		metadataReader:  nil,
		trackUpdateChan: nil,
	}

	// Initialize the Bubble Tea list component with styled delegate
	delegate := newStyledDelegate(&m.playing)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false) // We render our own header with column titles
	l.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(subtleColor)
	l.Styles.HelpStyle = lipgloss.NewStyle().Foreground(subtleColor).Padding(0, 0, 0, 2)
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return []key.Binding{
			key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		}
	}
	m.list = l

	// Start the Bubble Tea program with window size handling
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		os.Exit(1)
	}
}
