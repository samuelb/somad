package app

import (
	"testing"

	"somatui/internal/channels"
	"somatui/internal/state"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
)


// mockPlayer is a test double for the audio.Player interface.
type mockPlayer struct {
	playing bool
	playErr error
	errChan chan error
}

func (p *mockPlayer) Play(_ string) error {
	if p.playErr != nil {
		return p.playErr
	}
	p.playing = true
	return nil
}

func (p *mockPlayer) Stop() { p.playing = false }

func (p *mockPlayer) Errors() <-chan error { return p.errChan }

// newMockPlayer returns a mockPlayer with a buffered error channel.
func newMockPlayer() *mockPlayer {
	return &mockPlayer{errChan: make(chan error, 2)}
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

// setStateDir redirects state persistence to a temp directory for the duration of t.
func setStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// newTestModel returns a minimal Model populated with testChannels().
// It sets up a temporary state directory so SaveState calls succeed.
func newTestModel(t *testing.T) *Model {
	t.Helper()
	setStateDir(t)

	m := &Model{
		Player:       newMockPlayer(),
		State:        &state.State{},
		Width:        80,
		Height:       24,
		UserAgent:    "SomaTUI/test",
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
