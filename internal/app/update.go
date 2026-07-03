package app

import (
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"somatui/internal/audio"
	"somatui/internal/platform"
	"somatui/internal/state"
	"somatui/internal/ui"
	"somatui/pkg/playlist"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Update handles incoming messages and updates the model's state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	items := m.List.Items()
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle search input mode
		if m.Searching {
			switch msg.String() {
			case "ctrl+c":
				return m, m.quitApp()
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
					_, size := utf8.DecodeLastRuneInString(m.SearchQuery)
					m.SearchQuery = m.SearchQuery[:len(m.SearchQuery)-size]
					m.UpdateSearchMatches()
				}
				return m, nil
			default:
				// Append printable characters (including non-ASCII) to the query.
				if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
					if input := PrintableRunes(msg.Runes); input != "" {
						m.SearchQuery += input
						m.UpdateSearchMatches()
					}
				}
				return m, nil
			}
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, m.quitApp()
		case "enter", " ":
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		case "s":
			m.stopPlayback()
		case "a":
			// Toggle the inline about footer.
			m.ShowAbout = !m.ShowAbout
			m.UpdateListSize()
			return m, nil
		case "esc":
			// Close the about footer if it is open; otherwise fall through to the list.
			if m.ShowAbout {
				m.ShowAbout = false
				m.UpdateListSize()
				return m, nil
			}
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
		if m.State != nil {
			m.selectChannelByID(m.State.LastSelectedChannelID)
		}

		// If loaded from cache, refresh from network in background
		if msg.FromCache {
			return m, func() tea.Msg { return RefreshChannels(m.UserAgent) }
		}
	case ChannelsRefreshedMsg:
		// Channels have been refreshed from network, update the list.
		// A successful refresh also recovers from an earlier load failure,
		// so clear any error that would keep the error screen visible.
		m.Err = nil
		// Remember selected channel by ID for stable restoration after sort
		var selectedChannelID string
		if sel, ok := m.List.SelectedItem().(ui.Item); ok {
			selectedChannelID = sel.Channel.ID
		}
		newItems := ChannelsToItems(msg.Channels.Channels)
		newItems = m.sortItemsWithFavorites(newItems)
		m.List.SetItems(newItems)
		m.selectChannelByID(selectedChannelID)
	case ChannelRefreshTickMsg:
		// Time to refresh channels, fetch from network and schedule next tick
		return m, tea.Batch(func() tea.Msg { return RefreshChannels(m.UserAgent) }, TickChannelRefresh())
	case ErrorMsg:
		// An error occurred during channel loading
		m.Err = msg.Err
		m.Loading = false
	case TrackUpdateMsg:
		// Track information has been updated; ignore it (and stop polling)
		// once playback has ended.
		if m.PlayingID == "" {
			return m, nil
		}
		m.TrackInfo = &msg.TrackInfo
		m.UpdateMPRIS(items)
		return m, m.PollTrackUpdates()
	case TrackPollTickMsg:
		// Keep polling only while something is playing.
		if m.PlayingID == "" {
			return m, nil
		}
		return m, m.PollTrackUpdates()
	case PlaybackStartedMsg:
		if msg.ChannelID != m.ConnectingID {
			// Stale result: a newer play/stop request superseded this one.
			return m, nil
		}
		m.ConnectingID = ""
		m.PlayingID = msg.ChannelID
		m.TrackInfo = nil
		m.UpdateMPRIS(items)
		return m, m.PollTrackUpdates()
	case StreamErrorMsg:
		// Ignore errors from play requests that have been superseded; only the
		// active connect attempt (or the running stream, ChannelID == "") counts.
		if msg.ChannelID != "" && msg.ChannelID != m.ConnectingID {
			return m, nil
		}
		// Stop the player so the failed session's goroutine and audio
		// resources are released instead of lingering until the next play.
		m.stopPlayback()
		m.StreamErr = msg.Err.Error()
		return m, m.ListenStreamErrors()

	// MPRIS control messages
	case platform.MPRISPlayMsg:
		// Play the currently selected channel unless already playing/connecting
		if m.PlayingID == "" && m.ConnectingID == "" {
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playChannel(i)
			}
		}
	case platform.MPRISStopMsg:
		if m.PlayingID != "" {
			m.stopPlayback()
		}
	case platform.MPRISPlayPauseMsg:
		// Toggle: if playing or connecting, stop; if stopped, play
		if m.PlayingID != "" || m.ConnectingID != "" {
			m.stopPlayback()
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

// playChannel starts connecting to the given channel. The playlist fetch and
// stream connect run in the returned command so the UI stays responsive; the
// result arrives as PlaybackStartedMsg or StreamErrorMsg.
func (m *Model) playChannel(i ui.Item) tea.Cmd {
	m.StreamErr = ""

	if m.State != nil {
		m.State.LastSelectedChannelID = i.Channel.ID
		if err := state.SaveState(m.State); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving state: %v\n", err)
		}
	}

	channelID := i.Channel.ID
	m.ConnectingID = channelID
	playlistURL := SelectMP3PlaylistURL(i.Channel.Playlists)
	if playlistURL == "" {
		return func() tea.Msg {
			return StreamErrorMsg{
				Err:       fmt.Errorf("no MP3 playlist available for %s", i.Channel.Title),
				ChannelID: channelID,
			}
		}
	}

	player := m.Player
	userAgent := m.UserAgent
	return func() tea.Msg {
		streamURL, err := playlist.GetStreamURLFromPlaylist(playlistURL, userAgent)
		if err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("failed to get stream URL: %w", err), ChannelID: channelID}
		}
		if err := player.Play(streamURL); err != nil {
			if errors.Is(err, audio.ErrSuperseded) {
				// A newer play/stop request won; its own messages drive the UI.
				return nil
			}
			return StreamErrorMsg{Err: fmt.Errorf("failed to start playback: %w", err), ChannelID: channelID}
		}
		return PlaybackStartedMsg{ChannelID: channelID}
	}
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
