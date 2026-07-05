package app

import (
	"errors"

	"somatui/internal/protocol"
	"somatui/internal/ui"

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
	// VersionSkew names the server version when it differs from the client's.
	VersionSkew string

	Loading   bool
	Err       error
	ShowAbout bool
	About     AboutInfo
	Width     int
	Height    int
	// Search state
	Searching     bool   // Whether search input is active
	SearchQuery   string // Current search query
	SearchMatches []int  // Indices of matching items
	CurrentMatch  int    // Current position in searchMatches (-1 if none)
}

// Init requests the initial catalog and playback state from the server.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.fetchChannels(), m.fetchStatus(), tea.EnterAltScreen)
}

// applySnapshot installs a playback snapshot and derives the delegate's
// playing marker from it.
func (m *Model) applySnapshot(st protocol.PlaybackState) {
	m.Snapshot = st
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
	m.Loading = false
	m.Favorites = payload.Favorites

	var selectedID string
	if sel, ok := m.List.SelectedItem().(ui.Item); ok {
		selectedID = sel.Channel.ID
	}
	m.List.SetItems(m.sortItemsWithFavorites(ChannelsToItems(payload.Channels)))

	if firstLoad && selectedID == "" {
		m.selectChannelByID(payload.LastChannelID)
	} else {
		m.selectChannelByID(selectedID)
	}
	// Update search matches since indices may have changed
	if m.SearchQuery != "" {
		m.UpdateSearchMatches()
	}
}

// selectChannelByID moves the list cursor to the channel with the given ID,
// if present. Used to keep the selection stable across list re-sorts.
func (m *Model) selectChannelByID(id string) {
	if id == "" {
		return
	}
	for i, li := range m.List.Items() {
		if it, ok := li.(ui.Item); ok && it.Channel.ID == id {
			m.List.Select(i)
			return
		}
	}
}

// volumeStep is how much the +/- keys change the volume.
const volumeStep = 0.05
