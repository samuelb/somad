package app

import (
	"fmt"
	"os"

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
	// Reconnect state after a stream drop
	ReconnectingID   string // channel awaiting an automatic reconnect, empty if none
	ReconnectAttempt int    // number of reconnect attempts for the current drop
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

// stopPlayback stops the player (cancelling any in-flight connect), cancels
// any pending reconnect, and clears all playback-related state, then reflects
// the stopped state to MPRIS.
func (m *Model) stopPlayback() {
	if m.Player != nil {
		m.Player.Stop()
	}
	m.PlayingID = ""
	m.ConnectingID = ""
	m.ReconnectingID = ""
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

// volumeStep is how much the +/- keys change the volume.
const volumeStep = 0.05

// applyVolume sets the playback volume (clamped to [0, 1]), persists it, and
// optionally mirrors it to MPRIS (skipped when the change came from MPRIS).
func (m *Model) applyVolume(v float64, updateMPRIS bool) {
	if m.Player == nil {
		return
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	m.Player.SetVolume(v)
	if m.State != nil {
		m.State.SetVolume(v)
		if err := state.SaveState(m.State); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving state: %v\n", err)
		}
	}
	if updateMPRIS && m.MPRIS != nil {
		m.MPRIS.SetVolume(v)
	}
}

// adjustVolume changes the playback volume by delta.
func (m *Model) adjustVolume(delta float64) {
	if m.Player == nil {
		return
	}
	m.applyVolume(m.Player.Volume()+delta, true)
}

// findChannelItem returns the list item for the channel with the given ID.
func (m *Model) findChannelItem(id string) (ui.Item, bool) {
	for _, li := range m.List.Items() {
		if it, ok := li.(ui.Item); ok && it.Channel.ID == id {
			return it, true
		}
	}
	return ui.Item{}, false
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
