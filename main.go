package main

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// item implements the list.Item interface for displaying channels.
type item struct {
	channel Channel
}

// Title returns the title of the channel for display in the list.
func (i item) Title() string { return i.channel.Title }

// Description returns the description of the channel for display in the list.
func (i item) Description() string { return i.channel.Description }

// FilterValue returns the title of the channel for filtering purposes.
func (i item) FilterValue() string { return i.channel.Title }

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
							m.status = fmt.Sprintf("Playing: %s", i.channel.Title)

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
		// Leave space for status line and instructions
		m.list.SetSize(msg.Width, msg.Height-3)
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
	case errorMsg:
		// An error occurred during channel loading
		m.err = msg.err
		m.loading = false
	case trackUpdateMsg:
		// Track information has been updated
		m.trackInfo = &msg.trackInfo
		// Continue polling for updates
		return m, m.pollTrackUpdates()
	}

	// Update the list component and return its command
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// View renders the application's UI.
func (m *model) View() string {
	// Display loading message if channels are still being fetched
	if m.loading {
		return "Loading channels..."
	}
	// Display error message if an error occurred
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	// Build the view
	view := m.list.View()

	// Add status line with track information on the same line
	statusLine := m.status
	if m.trackInfo != nil {
		statusLine += fmt.Sprintf(" | 🎵 %s", m.trackInfo.Title)
	}
	view += "\n" + statusLine

	view += "\nPress 's' to stop, 'q' to quit."
	return view
}

// loadChannels is a Tea command that fetches SomaFM channels asynchronously.
func loadChannels() tea.Msg {
	channels, err := getChannels()
	if err != nil {
		return errorMsg{err}
	}
	return channelsLoadedMsg{channels}
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

	// Initialize the Bubble Tea list component
	l := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	l.Title = "SomaFM Stations"

	// Create the main application model
	m := &model{
		list:            l,
		player:          player,
		playing:         -1,
		loading:         true,
		config:          config,
		trackInfo:       nil,
		metadataReader:  nil,
		trackUpdateChan: nil,
	}

	// Start the Bubble Tea program with window size handling
	p := tea.NewProgram(m, tea.WithAltScreen())
	if err := p.Start(); err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		os.Exit(1)
	}
}
