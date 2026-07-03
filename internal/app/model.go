package app

import (
	"somatui/internal/audio"
	"somatui/internal/channels"
	"somatui/internal/platform"
	"somatui/internal/state"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// AboutInfo holds version and metadata for the about screen.
type AboutInfo struct {
	Version string
	Commit  string
	Date    string
}

// Model represents the application's state.
type Model struct {
	List         list.Model
	Player       audio.Player
	PlayingID    string // ID of the playing channel, empty if not playing
	ConnectingID string // ID of the channel currently connecting, empty if none
	Loading      bool
	Err          error
	State        *state.State
	TrackInfo    *audio.TrackInfo
	StreamErr    string
	ShowAbout    bool
	About        AboutInfo
	Width        int
	Height       int
	// Search state
	Searching     bool   // Whether search input is active
	SearchQuery   string // Current search query
	SearchMatches []int  // Indices of matching items
	CurrentMatch  int    // Current position in searchMatches (-1 if none)
	// MPRIS integration
	MPRIS *platform.MPRIS
	// User agent for HTTP requests
	UserAgent string
}

// Init initializes the application, loading channels asynchronously.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(LoadChannels(m.UserAgent), tea.EnterAltScreen, TickChannelRefresh(), m.ListenStreamErrors())
}

// stopPlayback stops the player (cancelling any in-flight connect) and clears
// all playback-related state, then reflects the stopped state to MPRIS.
func (m *Model) stopPlayback() {
	if m.Player != nil {
		m.Player.Stop()
	}
	m.PlayingID = ""
	m.ConnectingID = ""
	m.TrackInfo = nil
	m.StreamErr = ""
	m.UpdateMPRIS(m.List.Items())
}

// quitApp stops playback and returns the quit command.
func (m *Model) quitApp() tea.Cmd {
	if m.Player != nil {
		m.Player.Stop()
	}
	return tea.Quit
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

// mp3QualityRank orders SomaFM playlist quality levels, best first.
var mp3QualityRank = map[string]int{"highest": 0, "high": 1, "low": 2}

// SelectMP3PlaylistURL returns the best-quality MP3 playlist URL from a
// channel's playlists (highest > high > low > unknown), or "" if none.
func SelectMP3PlaylistURL(playlists []channels.Playlist) string {
	bestURL := ""
	bestRank := len(mp3QualityRank) + 1
	for _, playlist := range playlists {
		if playlist.Format != "mp3" {
			continue
		}
		rank, ok := mp3QualityRank[playlist.Quality]
		if !ok {
			rank = len(mp3QualityRank)
		}
		if rank < bestRank {
			bestURL = playlist.URL
			bestRank = rank
		}
	}
	return bestURL
}

// GetPlayingChannel returns the currently playing channel, or nil if not playing.
func (m *Model) GetPlayingChannel(items []list.Item) *channels.Channel {
	if m.PlayingID == "" {
		return nil
	}
	for _, listItem := range items {
		if i, ok := listItem.(ui.Item); ok && i.Channel.ID == m.PlayingID {
			return &i.Channel
		}
	}
	return nil
}
