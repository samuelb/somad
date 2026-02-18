package app

import (
	"somatui/internal/audio"
	"somatui/internal/channels"
	"somatui/internal/platform"
	"somatui/internal/state"

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
	return tea.Batch(LoadChannels, tea.EnterAltScreen, TickChannelRefresh())
}

// StopMetadataReader stops any active metadata reader.
func (m *Model) StopMetadataReader() {
	if m.MetadataReader != nil {
		m.MetadataReader.Stop()
		m.MetadataReader = nil
	}
}

// SelectMP3PlaylistURL finds the first MP3 playlist URL from a channel's playlists.
func SelectMP3PlaylistURL(playlists []channels.Playlist) string {
	for _, playlist := range playlists {
		if playlist.Format == "mp3" {
			return playlist.URL
		}
	}
	return ""
}

// GetPlayingChannel returns the currently playing channel, or nil if not playing.
func (m *Model) GetPlayingChannel(items []list.Item) *channels.Channel {
	if m.PlayingID == "" {
		return nil
	}
	for _, listItem := range items {
		if i, ok := listItem.(Item); ok && i.Channel.ID == m.PlayingID {
			return &i.Channel
		}
	}
	return nil
}

// Item wraps a channels.Channel for use with the list component.
type Item struct {
	Channel channels.Channel
}

// FilterValue returns the title for filtering.
func (i Item) FilterValue() string { return i.Channel.Title }

// Title returns the channel title.
func (i Item) Title() string { return i.Channel.Title }

// Description returns the channel description.
func (i Item) Description() string { return i.Channel.Description }
