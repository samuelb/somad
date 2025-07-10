package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type item struct {
	channel Channel
}

func (i item) Title() string       { return i.channel.Title }
func (i item) Description() string { return i.channel.Description }
func (i item) FilterValue() string { return i.channel.Title }

type model struct {
	list     list.Model
	player   *Player
	playing  int // Index of the playing channel, -1 if not playing
	status   string
	loading  bool
	err      error
	config   *Config
}

type channelsLoadedMsg struct {
	channels *Channels
}

type errorMsg struct {
	err error
}

func (m *model) Init() tea.Cmd {
	return loadChannels
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.player != nil {
				m.player.Stop()
			}
			return m, tea.Quit
		case "enter", " ":
			if i, ok := m.list.SelectedItem().(item); ok {
				m.playing = m.list.Index()

				// Save last selected channel
				if m.config != nil {
					m.config.LastSelectedChannelID = i.channel.ID
					if err := SaveConfig(m.config); err != nil {
						// Log error, but don't stop app
						fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
					}
				}

				// Find the highest quality mp3 stream
				var playlistURL string
				for _, playlist := range i.channel.Playlists {
					if playlist.Format == "mp3" {
						playlistURL = playlist.URL // a bit naive, but will work for now
					}
				}
				if playlistURL != "" {
					streamURL, err := getStreamURLFromPlaylist(playlistURL)
					if err != nil {
						m.status = fmt.Sprintf("Error getting stream URL: %v", err)
					} else {
						err := m.player.Play(streamURL)
						if err != nil {
							m.status = fmt.Sprintf("Error playing: %v", err)
						} else {
							m.status = fmt.Sprintf("Playing: %s", i.channel.Title)
						}
					}
				} else {
					m.status = "No MP3 stream found for this channel."
				}
			}
		case "s":
			if m.player != nil {
				m.player.Stop()
				m.playing = -1
				m.status = "Stopped"
			}
		}
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil

	case channelsLoadedMsg:
		items := make([]list.Item, len(msg.channels.Channels))
		for i, ch := range msg.channels.Channels {
			items[i] = item{channel: ch}
		}
		m.list.SetItems(items)
		m.loading = false

		// Set cursor to last selected channel
		if m.config != nil && m.config.LastSelectedChannelID != "" {
			for i, ch := range msg.channels.Channels {
				if ch.ID == m.config.LastSelectedChannelID {
					m.list.Select(i)
					break
				}
			}
		}
	case errorMsg:
		m.err = msg.err
		m.loading = false
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *model) View() string {
	if m.loading {
		return "Loading channels..."
	}
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}
	return m.list.View() + fmt.Sprintf("\n%s\n\nPress 's' to stop, 'q' to quit.\n", m.status)
}

func loadChannels() tea.Msg {
	channels, err := getChannels()
	if err != nil {
		return errorMsg{err}
	}
	return channelsLoadedMsg{channels}
}

func main() {
	player, err := NewPlayer()
	if err != nil {
		fmt.Printf("Alas, there's been an error initializing the player: %v\n", err)
		os.Exit(1)
	}

	config, err := LoadConfig()
	if err != nil {
		fmt.Printf("Alas, there's been an error loading config: %v\n", err)
		os.Exit(1)
	}

	l := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	l.Title = "SomaFM Stations"

	m := &model{
		list:    l,
		player:  player,
		playing: -1,
		loading: true,
		config:  config,
	}

	p := tea.NewProgram(m)
	if err := p.Start(); err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		os.Exit(1)
	}
}