package app

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"somatui/internal/audio"
	"somatui/internal/platform"
	"somatui/internal/state"
	"somatui/internal/ui"
	"somatui/pkg/playlist"
)

// Update handles incoming messages and updates the model's state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	items := m.List.Items()
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle about screen dismissal
		if m.ShowAbout {
			switch msg.String() {
			case "ctrl+c":
				return m, tea.Quit
			default:
				m.ShowAbout = false
				return m, nil
			}
		}

		// Handle search input mode
		if m.Searching {
			switch msg.String() {
			case "ctrl+c":
				if m.Player != nil {
					m.Player.Stop()
				}
				m.StopMetadataReader()
				return m, tea.Quit
			case "enter":
				// Exit search mode, keep at current match
				m.Searching = false
				m.UpdateListSize()
				return m, nil
			case "esc":
				// Cancel search, clear query
				m.ClearSearch()
				m.UpdateListSize()
				return m, nil
			case "backspace":
				if len(m.SearchQuery) > 0 {
					m.SearchQuery = m.SearchQuery[:len(m.SearchQuery)-1]
					m.UpdateSearchMatches()
				}
				return m, nil
			default:
				// Add valid printable characters to search query
				if len(msg.String()) == 1 && IsValidSearchChar(msg.String()[0]) {
					m.SearchQuery += msg.String()
					m.UpdateSearchMatches()
				}
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c", "q":
			if m.Player != nil {
				m.Player.Stop()
			}
			m.StopMetadataReader()
			return m, tea.Quit
		case "enter", " ":
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		case "s":
			if m.Player != nil {
				m.Player.Stop()
				m.PlayingID = ""
				m.StopMetadataReader()
				m.TrackInfo = nil
				m.UpdateMPRIS(items)
			}
		case "a":
			m.ShowAbout = true
			return m, nil
		case "/":
			// Enter search mode
			m.Searching = true
			m.SearchQuery = ""
			m.SearchMatches = nil
			m.CurrentMatch = -1
			m.UpdateListSize()
			return m, nil
		case "n":
			// Next match
			if len(m.SearchMatches) > 0 {
				m.NextMatch()
				return m, nil
			}
		case "N":
			// Previous match
			if len(m.SearchMatches) > 0 {
				m.PrevMatch()
				return m, nil
			}
		case "f", "*":
			// Toggle favorite on selected channel
			m.ToggleFavorite()
			return m, nil
		case "c":
			// Clear search
			if m.SearchQuery != "" {
				m.ClearSearch()
				m.UpdateListSize()
				return m, nil
			}
		}
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.UpdateListSize()
		return m, nil

	case ChannelsLoadedMsg:
		// Channels have been loaded, update the list and stop loading indicator
		newItems := ChannelsToItems(msg.Channels.Channels)
		newItems = m.sortItemsWithFavorites(newItems)
		m.List.SetItems(newItems)
		m.Loading = false

		// Set the cursor to the last selected channel if available
		if m.State != nil && m.State.LastSelectedChannelID != "" {
			for i, li := range newItems {
				if it, ok := li.(ui.Item); ok && it.Channel.ID == m.State.LastSelectedChannelID {
					m.List.Select(i)
					break
				}
			}
		}

		// If loaded from cache, refresh from network in background
		if msg.FromCache {
			return m, func() tea.Msg { return RefreshChannels(m.UserAgent) }
		}
	case ChannelsRefreshedMsg:
		// Channels have been refreshed from network, update the list
		// Remember selected channel by ID for stable restoration after sort
		var selectedChannelID string
		if sel, ok := m.List.SelectedItem().(ui.Item); ok {
			selectedChannelID = sel.Channel.ID
		}
		newItems := ChannelsToItems(msg.Channels.Channels)
		newItems = m.sortItemsWithFavorites(newItems)
		m.List.SetItems(newItems)

		// Restore selection by channel ID
		for i, li := range newItems {
			if it, ok := li.(ui.Item); ok && it.Channel.ID == selectedChannelID {
				m.List.Select(i)
				break
			}
		}
	case ChannelRefreshTickMsg:
		// Time to refresh channels, fetch from network and schedule next tick
		return m, tea.Batch(func() tea.Msg { return RefreshChannels(m.UserAgent) }, TickChannelRefresh())
	case ErrorMsg:
		// An error occurred during channel loading
		m.Err = msg.Err
		m.Loading = false
	case TrackUpdateMsg:
		// Track information has been updated
		m.TrackInfo = &msg.TrackInfo
		m.UpdateMPRIS(items)
	case StreamErrorMsg:
		m.PlayingID = ""
		m.UpdateMPRIS(items)

	// MPRIS control messages
	case platform.MPRISPlayMsg:
		// Play the currently selected channel
		if m.PlayingID == "" {
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		}
	case platform.MPRISStopMsg:
		if m.Player != nil && m.PlayingID != "" {
			m.Player.Stop()
			m.PlayingID = ""
			m.StopMetadataReader()
			m.TrackInfo = nil
			m.UpdateMPRIS(items)
		}
	case platform.MPRISPlayPauseMsg:
		// Toggle: if playing, stop; if stopped, play
		if m.PlayingID != "" {
			m.Player.Stop()
			m.PlayingID = ""
			m.StopMetadataReader()
			m.TrackInfo = nil
			m.UpdateMPRIS(items)
		} else {
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		}
	case platform.MPRISNextMsg:
		// Move to next channel and play
		listItems := m.List.Items()
		if len(listItems) > 0 {
			currentIdx := m.List.Index()
			nextIdx := (currentIdx + 1) % len(listItems)
			m.List.Select(nextIdx)
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		}
	case platform.MPRISPrevMsg:
		// Move to previous channel and play
		listItems := m.List.Items()
		if len(listItems) > 0 {
			currentIdx := m.List.Index()
			prevIdx := currentIdx - 1
			if prevIdx < 0 {
				prevIdx = len(listItems) - 1
			}
			m.List.Select(prevIdx)
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		}
	}

	// Update the list component and return its command
	var cmd tea.Cmd
	m.List, cmd = m.List.Update(msg)
	return m, cmd
}

// playChannel starts playing the given channel.
func (m *Model) playChannel(i ui.Item) tea.Cmd {
	m.PlayingID = i.Channel.ID

	// Save the last selected channel
	if m.State != nil {
		m.State.LastSelectedChannelID = i.Channel.ID
		_ = state.SaveState(m.State) // Ignore error - continue anyway
	}

	playlistURL := SelectMP3PlaylistURL(i.Channel.Playlists)
	if playlistURL == "" {
		return nil
	}

	streamURL, err := playlist.GetStreamURLFromPlaylist(playlistURL, m.UserAgent)
	if err != nil {
		return nil
	}

	if err := m.Player.Play(streamURL); err != nil {
		return nil
	}

	m.StopMetadataReader()
	m.MetadataReader = audio.NewMetadataReader(streamURL)
	m.MetadataReader.Start(m.UserAgent)
	m.TrackInfo = nil

	// Update MPRIS
	m.UpdateMPRIS(m.List.Items())

	return m.PollTrackUpdates()
}

// NewHelpKeys returns additional help keys for the list.
func NewHelpKeys() ([]key.Binding, []key.Binding) {
	fullHelp := []key.Binding{
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f/*", "toggle favorite")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n/N", "next/prev match")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
	}

	shortHelp := []key.Binding{
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f/*", "toggle favorite")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
	}

	return fullHelp, shortHelp
}
