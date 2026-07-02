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
	List           list.Model
	Player         audio.Player
	PlayingID      string // ID of the playing channel, empty if not playing
	Loading        bool
	Err            error
	State          *state.State
	TrackInfo      *audio.TrackInfo
	MetadataReader *audio.MetadataReader
	StreamErr      string
	ShowAbout      bool
	About          AboutInfo
	Width          int
	Height         int
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

// StopMetadataReader stops any active metadata reader.
func (m *Model) StopMetadataReader() {
	if m.MetadataReader != nil {
		m.MetadataReader.Stop()
		m.MetadataReader = nil
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
