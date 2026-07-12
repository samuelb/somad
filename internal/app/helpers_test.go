package app

import (
	"slices"
	"sync"
	"testing"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeBackend is a test double for the Backend interface. It records calls
// and answers with server-like snapshots.
type fakeBackend struct {
	mu        sync.Mutex
	playIDs   []string
	stops     int
	shutdowns int
	volumes   []float64
	favorites []string
	status    protocol.PlaybackState
	payload   protocol.ChannelsPayload
	// callErr, when set, fails every request method; shutdownErr fails
	// Shutdown specifically.
	callErr     error
	shutdownErr error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		status: protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1},
	}
}

func (b *fakeBackend) Status() (protocol.PlaybackState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callErr != nil {
		return protocol.PlaybackState{}, b.callErr
	}
	return b.status, nil
}

func (b *fakeBackend) Channels() (protocol.ChannelsPayload, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callErr != nil {
		return protocol.ChannelsPayload{}, b.callErr
	}
	return b.payload, nil
}

func (b *fakeBackend) Play(channelID string) (protocol.PlaybackState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callErr != nil {
		return protocol.PlaybackState{}, b.callErr
	}
	b.playIDs = append(b.playIDs, channelID)
	b.status = protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: channelID, Volume: b.status.Volume}
	return b.status, nil
}

func (b *fakeBackend) Stop() (protocol.PlaybackState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callErr != nil {
		return protocol.PlaybackState{}, b.callErr
	}
	b.stops++
	b.status = protocol.PlaybackState{Status: protocol.StatusStopped, Volume: b.status.Volume}
	return b.status, nil
}

func (b *fakeBackend) SetVolume(v float64) (protocol.PlaybackState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callErr != nil {
		return protocol.PlaybackState{}, b.callErr
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	b.volumes = append(b.volumes, v)
	b.status.Volume = v
	return b.status, nil
}

func (b *fakeBackend) Shutdown() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.shutdowns++
	return b.shutdownErr
}

func (b *fakeBackend) ToggleFavorite(channelID string) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.callErr != nil {
		return nil, b.callErr
	}
	if i := slices.Index(b.favorites, channelID); i >= 0 {
		b.favorites = slices.Delete(b.favorites, i, i+1)
	} else {
		b.favorites = append(b.favorites, channelID)
	}
	return slices.Clone(b.favorites), nil
}

// testChannels returns a fixed set of channels used across test files.
func testChannels() []channels.Channel {
	return []channels.Channel{
		{
			ID:          "groovesalad",
			Title:       "Groove Salad",
			Description: "A nicely chilled plate of ambient beats",
			Genre:       "ambient",
			Listeners:   "1000",
			Playlists:   []channels.Playlist{{URL: "http://somafm.com/groovesalad.pls", Format: "mp3"}},
		},
		{
			ID:          "dronezone",
			Title:       "Drone Zone",
			Description: "Atmospheric texture and ambient space music",
			Genre:       "ambient|space",
			Listeners:   "500",
			Playlists:   []channels.Playlist{{URL: "http://somafm.com/dronezone.pls", Format: "mp3"}},
		},
		{
			ID:          "secretagent",
			Title:       "Secret Agent",
			Description: "The soundtrack for your spy movie marathon",
			Genre:       "lounge|spy",
			Listeners:   "750",
			Playlists:   []channels.Playlist{{URL: "http://somafm.com/secretagent.pls", Format: "mp3"}},
		},
	}
}

// newTestModel returns a minimal Model populated with testChannels() and a
// fake backend.
func newTestModel(t *testing.T) *Model {
	t.Helper()

	m := &Model{
		Backend:      newFakeBackend(),
		Snapshot:     protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1},
		Width:        80,
		Height:       24,
		CurrentMatch: -1,
	}

	items := ChannelsToItems(testChannels())
	delegate := ui.NewStyledDelegate(&m.PlayingID, m.IsMatch, m.IsFavorite)
	l := list.New(items, delegate, 80, 24)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	m.List = l

	return m
}

// backend returns the model's fake backend.
func backend(m *Model) *fakeBackend {
	return m.Backend.(*fakeBackend)
}

// runCmd executes a tea.Cmd synchronously, returning its message.
func runCmd(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}
