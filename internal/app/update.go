package app

import (
	"fmt"
	"unicode/utf8"

	"somad/internal/protocol"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// Update handles incoming messages and updates the model's state.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.Searching {
			return m, m.updateSearchKey(msg)
		}
		if cmd, handled := m.updateListKey(msg); handled {
			return m, cmd
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		m.UpdateListSize()
		return m, nil

	case tea.MouseMsg:
		// The vendored bubbles/list does not handle mouse events itself, so
		// the wheel is wired to the same cursor movement as j/k here.
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.List.CursorUp()
			return m, nil
		case tea.MouseButtonWheelDown:
			m.List.CursorDown()
			return m, nil
		}

	case ServerStateMsg:
		m.applySnapshot(msg.State)
		return m, nil

	case ServerChannelsMsg:
		m.applyChannels(msg.Payload)
		return m, nil

	case FavoritesMsg:
		m.applyFavorites(msg.Favorites)
		return m, nil

	case HistoryMsg:
		// Drop a result for a fetch that started for a channel the overlay
		// has since moved on from (closed, or reopened for another channel);
		// applying it would clobber the current, still-loading or already
		// populated, view with a stale answer.
		if m.ShowHistory && msg.ChannelID == m.HistoryChannelID {
			m.History = msg.Entries
			m.HistoryErr = msg.Err
		}
		return m, nil

	case RequestErrorMsg:
		if m.Loading && msg.Op == opLoadChannels {
			// Without a catalog there is nothing to render behind a status
			// bar notice; show the error screen instead of loading forever.
			m.Loading = false
			m.Err = msg.Err
			return m, nil
		}
		m.RequestErr = fmt.Sprintf("%s failed: %v", msg.Op, msg.Err)
		return m, nil

	case RestartFailedMsg:
		// No reconnect will follow, so a queued channel change would never
		// play; drop it and tell the user instead of failing silently.
		m.pendingPlayID = ""
		m.RequestErr = fmt.Sprintf("server restart failed: %v", msg.Err)
		return m, nil

	case ServerLostMsg:
		m.ServerLost = true
		return m, nil

	case ServerReconnectedMsg:
		return m, m.applyReconnect(msg)

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

// updateSearchKey handles a key press while the search input is active:
// the query is edited in place, and enter/esc leave search mode.
func (m *Model) updateSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quitCmd()
	case "enter":
		// Exit search mode, keep at current match
		m.Searching = false
		m.UpdateListSize()
	case "esc":
		// Cancel search, clear query
		m.ClearSearch()
		m.UpdateListSize()
	case "backspace":
		if len(m.SearchQuery) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.SearchQuery)
			m.SearchQuery = m.SearchQuery[:len(m.SearchQuery)-size]
			m.UpdateSearchMatches()
		}
	default:
		// Append printable characters (including non-ASCII) to the query.
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			if input := PrintableRunes(msg.Runes); input != "" {
				m.SearchQuery += input
				m.UpdateSearchMatches()
			}
		}
	}
	return nil
}

// updateListKey handles a key press in list mode against the keymap. It
// reports false when the key is not one of ours, or has nothing to act on
// in the current state (say, esc with no overlay open), so the caller can
// hand it to the list component instead.
func (m *Model) updateListKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch {
	case key.Matches(msg, keys.Quit):
		return m.quitCmd(), true
	case key.Matches(msg, keys.Play):
		id := m.selectedChannelID()
		if id == "" {
			return nil, false
		}
		// Changing channel interrupts the stream anyway, so an out-of-date
		// server is restarted first and the channel is played once the
		// reconnect delivers a fresh backend.
		if m.skewed() {
			m.pendingPlayID = id
			return m.restartCmd(), true
		}
		return m.playCmd(id), true
	case key.Matches(msg, keys.Stop):
		// Stopping interrupts the stream anyway; upgrade an out-of-date
		// server while we're at it (the fresh one comes up stopped).
		if m.skewed() && m.Snapshot.Status != protocol.StatusStopped {
			return m.restartCmd(), true
		}
		return m.stopCmd(), true
	case key.Matches(msg, keys.PlayPause):
		// Toggle play/pause without leaving the list.
		return m.playPauseCmd(), true
	case key.Matches(msg, keys.About):
		// Toggle the inline about footer.
		m.ShowAbout = !m.ShowAbout
		m.UpdateListSize()
		return nil, true
	case key.Matches(msg, keys.History):
		return m.toggleHistory(), true
	case key.Matches(msg, keys.Escape):
		// Close whichever overlay is open; otherwise the list gets the key.
		if !m.ShowHistory && !m.ShowAbout {
			return nil, false
		}
		m.ShowHistory, m.ShowAbout = false, false
		m.UpdateListSize()
		return nil, true
	case key.Matches(msg, keys.Search):
		// Enter search mode. An existing query (kept after Enter) is
		// pre-filled for editing rather than reset.
		m.Searching = true
		m.UpdateListSize()
		return nil, true
	case key.Matches(msg, keys.NextMatch):
		if m.matchCount() == 0 {
			return nil, false
		}
		m.NextMatch()
		return nil, true
	case key.Matches(msg, keys.PrevMatch):
		if m.matchCount() == 0 {
			return nil, false
		}
		m.PrevMatch()
		return nil, true
	case key.Matches(msg, keys.Favorite):
		// Toggle favorite on selected channel
		return m.ToggleFavorite(), true
	case key.Matches(msg, keys.FavoritesOnly):
		// Toggle a favorites-only view, applied on top of the search
		// filter via the shared refreshVisibleItems plumbing.
		m.FavoritesOnly = !m.FavoritesOnly
		m.refreshVisibleItems(m.selectedChannelID())
		return nil, true
	case key.Matches(msg, keys.VolumeUp):
		return m.setVolumeCmd(m.Snapshot.Volume + volumeStep), true
	case key.Matches(msg, keys.VolumeDown):
		return m.setVolumeCmd(m.Snapshot.Volume - volumeStep), true
	case key.Matches(msg, keys.Mute):
		return m.toggleMuteCmd(), true
	case key.Matches(msg, keys.ClearSearch):
		if m.SearchQuery == "" {
			return nil, false
		}
		m.ClearSearch()
		m.UpdateListSize()
		return nil, true
	}
	return nil, false
}

// toggleHistory opens or closes the now-playing history overlay for the
// playing channel, returning the fetch to run when it opens.
func (m *Model) toggleHistory() tea.Cmd {
	m.ShowHistory = !m.ShowHistory
	m.UpdateListSize()
	if !m.ShowHistory {
		return nil
	}
	m.HistoryChannelID = m.Snapshot.ChannelID
	m.HistoryChannelTitle = m.Snapshot.ChannelTitle
	m.HistoryErr = nil
	if m.HistoryChannelID == "" {
		// Nothing is playing: nothing to fetch.
		m.History = nil
		return nil
	}
	return m.historyCmd(m.HistoryChannelID)
}

// applyReconnect swaps in the fresh backend after a reconnect and returns
// the commands that resynchronize the model with it.
func (m *Model) applyReconnect(msg ServerReconnectedMsg) tea.Cmd {
	m.ServerLost = false
	m.Backend = msg.Backend
	m.ServerVersion = msg.ServerVersion
	// A channel change queued before a version-upgrade restart plays now
	// that a fresh backend is here.
	if m.pendingPlayID != "" {
		id := m.pendingPlayID
		m.pendingPlayID = ""
		return tea.Batch(m.fetchChannels(), m.playCmd(id))
	}
	return tea.Batch(m.fetchChannels(), m.fetchStatus())
}

// keyMap is the list-mode keymap: every binding's keys and help text in one
// place, so the dispatch in Update and the help lines from NewHelpKeys
// cannot drift apart. Keep it in step with the README and website tables
// (see "Where facts live" in AGENTS.md). Search-input mode (see Update) has
// its own few fixed keys.
type keyMap struct {
	Quit, Play, PlayPause, Stop, Favorite, FavoritesOnly, VolumeUp, VolumeDown, Mute,
	Search, NextMatch, PrevMatch, ClearSearch, About, History, Escape key.Binding
}

var keys = keyMap{
	Quit:          key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit (keeps playing)")),
	Play:          key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter/space", "play selected")),
	PlayPause:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "play/pause")),
	Stop:          key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
	Favorite:      key.NewBinding(key.WithKeys("f", "*"), key.WithHelp("f/*", "toggle favorite")),
	FavoritesOnly: key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "favorites-only view")),
	VolumeUp:      key.NewBinding(key.WithKeys("+", "="), key.WithHelp("+/-", "volume (also =/_)")),
	VolumeDown:    key.NewBinding(key.WithKeys("-", "_")),
	Mute:          key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mute")),
	Search:        key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter channels")),
	NextMatch:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n/N", "next/prev match")),
	PrevMatch:     key.NewBinding(key.WithKeys("N")),
	ClearSearch:   key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear search")),
	About:         key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "about")),
	History:       key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "history")),
	Escape:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "close about/history / cancel search")),
}

// withHelp returns b with its help line replaced, for the terser short-help
// bar.
func withHelp(b key.Binding, k, desc string) key.Binding {
	b.SetHelp(k, desc)
	return b
}

// NewHelpKeys returns additional help keys for the list: the full help
// (every list-mode binding) and the one-line short help.
func NewHelpKeys(shutdownOnExit bool) ([]key.Binding, []key.Binding) {
	quit := keys.Quit
	if shutdownOnExit {
		quit = withHelp(quit, "q", "quit (stops server)")
	}
	fullHelp := []key.Binding{
		keys.Play, keys.PlayPause, keys.Stop, keys.Favorite, keys.FavoritesOnly,
		keys.VolumeUp, keys.Mute, keys.Search, keys.NextMatch, keys.ClearSearch,
		keys.About, keys.History, keys.Escape, quit,
	}
	shortHelp := []key.Binding{
		withHelp(keys.Play, "enter", "play"), keys.PlayPause, keys.Stop, keys.Favorite,
		keys.Mute, withHelp(keys.Search, "/", "filter"), keys.About, keys.History,
	}
	return fullHelp, shortHelp
}
