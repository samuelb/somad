package app

import (
	"unicode/utf8"

	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Update handles incoming messages and updates the model's state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle search input mode
		if m.Searching {
			switch msg.String() {
			case "ctrl+c":
				// Quitting leaves the server (and any playback) running.
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
			// Quitting the TUI intentionally does not stop playback: the
			// server keeps streaming until stopped ('s') or it idles out.
			return m, tea.Quit
		case "enter", " ":
			if i, ok := m.List.SelectedItem().(ui.Item); ok {
				return m, m.playCmd(i.Channel.ID)
			}
		case "s":
			return m, m.stopCmd()
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
			return m, m.ToggleFavorite()
		case "+", "=":
			return m, m.setVolumeCmd(m.Snapshot.Volume + volumeStep)
		case "-", "_":
			return m, m.setVolumeCmd(m.Snapshot.Volume - volumeStep)
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

	case ServerStateMsg:
		m.applySnapshot(msg.State)
		return m, nil

	case ServerChannelsMsg:
		m.applyChannels(msg.Payload)
		return m, nil

	case ServerLostMsg:
		m.ServerLost = true
		return m, nil

	case ServerReconnectedMsg:
		m.ServerLost = false
		m.Backend = msg.Backend
		return m, tea.Batch(m.fetchChannels(), m.fetchStatus())

	case ServerGoneMsg:
		m.ServerLost = false
		m.Loading = false
		m.Err = msg.Err
		return m, nil
	}

	// Update the list component and return its command
	var cmd tea.Cmd
	m.List, cmd = m.List.Update(msg)
	return m, cmd
}

// NewHelpKeys returns additional help keys for the list.
func NewHelpKeys() ([]key.Binding, []key.Binding) {
	fullHelp := []key.Binding{
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f/*", "toggle favorite")),
		key.NewBinding(key.WithKeys("+"), key.WithHelp("+/-", "volume")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n/N", "next/prev match")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit (keeps playing)")),
	}

	shortHelp := []key.Binding{
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f/*", "toggle favorite")),
		key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
	}

	return fullHelp, shortHelp
}
