package app

import (
	"errors"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/ui"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/bubbles/list"
)

// AboutInfo holds version and metadata for the about screen.
type AboutInfo struct {
	Version string
	Commit  string
	Date    string
}

// Model represents the TUI state. Playback lives in the server; the model
// renders from the latest snapshot and sends commands over the Backend.
type Model struct {
	List    list.Model
	Backend Backend

	// Snapshot is the latest authoritative playback state from the server.
	Snapshot protocol.PlaybackState
	// Favorites mirrors the server-persisted favorite channel IDs.
	Favorites []string
	// PlayingID is derived from Snapshot for the list delegate's playing marker.
	PlayingID string
	// ServerLost is true while the server connection is being re-established.
	ServerLost bool
	// ServerVersion is the version the connected server reports. When it differs
	// from About.Version the server is out of date and the next channel change
	// or stop restarts it onto ours (see skewed).
	ServerVersion string
	// pendingPlayID is a channel to play once the server has been restarted for
	// a version upgrade and the reconnect has delivered a fresh backend.
	pendingPlayID string

	Loading bool
	Err     error
	// RequestErr is the most recent failed-request notice, shown in the
	// status bar until the server next answers successfully.
	RequestErr string
	ShowAbout  bool
	About      AboutInfo
	// ShowHistory toggles the now-playing history overlay. HistoryChannelID
	// and HistoryChannelTitle are the channel it was opened for; History
	// holds the last fetch's entries and HistoryErr its error, if any.
	ShowHistory         bool
	HistoryChannelID    string
	HistoryChannelTitle string
	History             []protocol.HistoryEntry
	HistoryErr          error
	Width               int
	Height              int
	// ShutdownOnExit asks the server to stop playback and exit when the TUI
	// closes. OnExit is called before quitting so the reconnect bridge does not
	// auto-spawn a replacement server.
	ShutdownOnExit bool
	OnExit         func()
	// Search state. While SearchQuery is set, m.List holds only the
	// matching channels (see refreshVisibleItems), so the match count is the
	// list length and the current match is the list cursor.
	Searching   bool   // Whether search input is active
	SearchQuery string // Current search query
	// FavoritesOnly restricts the list to favorite channels, applied on top
	// of the search filter above; see refreshVisibleItems.
	FavoritesOnly bool

	// allItems is the full, favorites-sorted catalog. m.List.Items() shows
	// either allItems or a filtered subset of it (search and/or
	// FavoritesOnly); see refreshVisibleItems in search.go.
	allItems []list.Item
}

// Init requests the initial catalog and playback state from the server.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.fetchChannels(), m.fetchStatus(), tea.EnterAltScreen)
}

// skewed reports whether the connected server runs a different version than the
// client, meaning the next channel change or stop should restart it onto ours.
func (m *Model) skewed() bool {
	return m.ServerVersion != "" && client.VersionSkewed(m.About.Version, m.ServerVersion)
}

// applySnapshot installs a playback snapshot and derives the delegate's
// playing marker from it.
func (m *Model) applySnapshot(st protocol.PlaybackState) {
	m.Snapshot = st
	m.RequestErr = ""
	if st.Status == protocol.StatusPlaying {
		m.PlayingID = st.ChannelID
	} else {
		m.PlayingID = ""
	}
}

// applyChannels installs a catalog payload: favorites, sorted items, stable
// selection, and the loading/error screens.
func (m *Model) applyChannels(payload protocol.ChannelsPayload) {
	if len(payload.Channels) == 0 {
		// The server had neither a cache nor a network catalog. Show its
		// error; an empty payload without one means the load is still
		// underway and a channels event will follow.
		if payload.Error != "" {
			m.Err = errors.New(payload.Error)
			m.Loading = false
		}
		return
	}

	firstLoad := m.Loading
	m.Err = nil
	m.RequestErr = ""
	m.Loading = false
	m.Favorites = payload.Favorites

	selectedID := m.selectedChannelID()
	m.allItems = m.sortItemsWithFavorites(ChannelsToItems(payload.Channels))

	if firstLoad && selectedID == "" {
		selectedID = payload.LastChannelID
	}
	// Recompute the visible (possibly filtered) list from the new catalog,
	// keeping the cursor on the same channel where possible.
	m.refreshVisibleItems(selectedID)
}

// matchCount is how many channels the active search query matches, or 0
// with no query.
func (m *Model) matchCount() int {
	if m.SearchQuery == "" {
		return 0
	}
	return len(m.List.Items())
}

// selectedChannelID returns the ID of the channel under the cursor, or ""
// when the list is empty. Callers capture it before re-sorting or
// re-filtering the list so refreshVisibleItems can keep the cursor there.
func (m *Model) selectedChannelID() string {
	if sel, ok := m.List.SelectedItem().(ui.Item); ok {
		return sel.Channel.ID
	}
	return ""
}

// selectChannelByID moves the list cursor to the channel with the given ID,
// if present, and reports whether it was found. Used to keep the selection
// stable across list re-sorts and re-filters.
func (m *Model) selectChannelByID(id string) bool {
	if id == "" {
		return false
	}
	for i, li := range m.List.Items() {
		if it, ok := li.(ui.Item); ok && it.Channel.ID == id {
			m.List.Select(i)
			return true
		}
	}
	return false
}

// volumeStep is how much the +/- keys change the volume.
const volumeStep = 0.05
