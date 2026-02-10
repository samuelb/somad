package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPlayer is a test double for AudioPlayer.
type mockPlayer struct {
	playURL    string
	playing    bool
	playCalled int
	stopCalled int
	playErr    error
}

func (m *mockPlayer) Play(url string) error {
	m.playCalled++
	m.playURL = url
	if m.playErr != nil {
		return m.playErr
	}
	m.playing = true
	return nil
}

func (m *mockPlayer) Stop() {
	m.stopCalled++
	m.playing = false
}

// testChannels returns a slice of channels for testing.
func testChannels() []Channel {
	return []Channel{
		{
			ID:          "groovesalad",
			Title:       "Groove Salad",
			Description: "A nicely chilled plate of ambient beats",
			Genre:       "ambient",
			Listeners:   "1000",
			Playlists: []Playlist{
				{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "high"},
				{URL: "http://somafm.com/groovesalad.pls", Format: "aac", Quality: "low"},
			},
		},
		{
			ID:          "dronezone",
			Title:       "Drone Zone",
			Description: "Atmospheric texture and ambient space music",
			Genre:       "ambient|space",
			Listeners:   "500",
			Playlists: []Playlist{
				{URL: "http://somafm.com/dronezone.pls", Format: "mp3", Quality: "high"},
			},
		},
		{
			ID:          "secretagent",
			Title:       "Secret Agent",
			Description: "The soundtrack for your spy movie marathon",
			Genre:       "lounge|spy",
			Listeners:   "750",
			Playlists: []Playlist{
				{URL: "http://somafm.com/secretagent.pls", Format: "mp3", Quality: "high"},
			},
		},
	}
}

// newTestModel creates a model with test channels and a mock player.
func newTestModel(channels []Channel) *model {
	items := make([]list.Item, len(channels))
	for i, ch := range channels {
		items[i] = item{channel: ch}
	}
	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, 80, 24)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	return &model{
		list:   l,
		player: &mockPlayer{},
		state:  &State{},
		width:  80,
		height: 24,
	}
}

// --- Pure function tests ---

func TestIsValidSearchChar(t *testing.T) {
	tests := []struct {
		name string
		char byte
		want bool
	}{
		{"letter a", 'a', true},
		{"letter Z", 'Z', true},
		{"digit 0", '0', true},
		{"space", ' ', true},
		{"exclamation", '!', true},
		{"tilde", '~', true},
		{"null byte", 0, false},
		{"tab", '\t', false},
		{"newline", '\n', false},
		{"backspace", '\b', false},
		{"DEL", 127, false},
		{"high byte", 128, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isValidSearchChar(tt.char))
		})
	}
}

func TestSelectMP3PlaylistURL(t *testing.T) {
	tests := []struct {
		name      string
		playlists []Playlist
		want      string
	}{
		{
			name: "finds mp3",
			playlists: []Playlist{
				{URL: "http://aac.example.com", Format: "aac"},
				{URL: "http://mp3.example.com", Format: "mp3"},
			},
			want: "http://mp3.example.com",
		},
		{
			name: "first mp3 wins",
			playlists: []Playlist{
				{URL: "http://first.example.com", Format: "mp3"},
				{URL: "http://second.example.com", Format: "mp3"},
			},
			want: "http://first.example.com",
		},
		{
			name: "no mp3",
			playlists: []Playlist{
				{URL: "http://aac.example.com", Format: "aac"},
			},
			want: "",
		},
		{
			name:      "empty list",
			playlists: nil,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, selectMP3PlaylistURL(tt.playlists))
		})
	}
}

func TestCalculateColumnWidths(t *testing.T) {
	tests := []struct {
		name       string
		totalWidth int
		wantLeft   int
		wantRight  int
	}{
		{"standard width", 80, 64, 12},
		{"narrow width", 30, 20, 12}, // leftCol clamped to minLeftColumnWidth
		{"very narrow", 10, 20, 12},  // leftCol clamped
		{"wide", 120, 104, 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, right := calculateColumnWidths(tt.totalWidth)
			assert.Equal(t, tt.wantLeft, left)
			assert.Equal(t, tt.wantRight, right)
		})
	}
}

func TestPlaceOverlay(t *testing.T) {
	bg := "AAAAAA\nBBBBBB\nCCCCCC"
	fg := "XX\nYY"

	result := placeOverlay(2, 0, fg, bg)
	lines := splitLines(result)
	require.Len(t, lines, 3)
	// Overlay places fg at x=2: left 2 chars + fg + remaining right part
	assert.Contains(t, lines[0], "AAXX")
	assert.Contains(t, lines[1], "BBYY")
	assert.Equal(t, "CCCCCC", lines[2]) // untouched
}

func TestPlaceOverlay_DistinctChars(t *testing.T) {
	bg := "ABCDEF\nGHIJKL\nMNOPQR"
	fg := "XX"

	result := placeOverlay(0, 1, fg, bg)
	lines := splitLines(result)
	require.Len(t, lines, 3)
	assert.Equal(t, "ABCDEF", lines[0])     // untouched
	assert.True(t, lines[1][0:2] == "XX")   // overlay at position 0
	assert.Equal(t, "MNOPQR", lines[2])     // untouched
}

func TestPlaceOverlay_OutOfBounds(t *testing.T) {
	bg := "AAA\nBBB"
	fg := "X"

	// fg at y=5 is out of bounds - should not panic
	result := placeOverlay(0, 5, fg, bg)
	assert.Equal(t, "AAA\nBBB", result)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

// --- Search logic tests ---

func TestUpdateSearchMatches(t *testing.T) {
	m := newTestModel(testChannels())

	m.searchQuery = "zone"
	m.updateSearchMatches()

	assert.Len(t, m.searchMatches, 1)
	assert.Equal(t, 1, m.searchMatches[0]) // "Drone Zone" is index 1
	assert.Equal(t, 0, m.currentMatch)
}

func TestUpdateSearchMatches_MultipleResults(t *testing.T) {
	m := newTestModel(testChannels())

	// "a" appears in all three titles/descriptions
	m.searchQuery = "ambient"
	m.updateSearchMatches()

	assert.GreaterOrEqual(t, len(m.searchMatches), 2)
}

func TestUpdateSearchMatches_NoResults(t *testing.T) {
	m := newTestModel(testChannels())

	m.searchQuery = "zzzznotfound"
	m.updateSearchMatches()

	assert.Empty(t, m.searchMatches)
	assert.Equal(t, -1, m.currentMatch)
}

func TestUpdateSearchMatches_EmptyQuery(t *testing.T) {
	m := newTestModel(testChannels())

	m.searchQuery = ""
	m.updateSearchMatches()

	assert.Empty(t, m.searchMatches)
	assert.Equal(t, -1, m.currentMatch)
}

func TestUpdateSearchMatches_CaseInsensitive(t *testing.T) {
	m := newTestModel(testChannels())

	m.searchQuery = "GROOVE"
	m.updateSearchMatches()

	assert.Len(t, m.searchMatches, 1)
	assert.Equal(t, 0, m.searchMatches[0])
}

func TestNextMatch(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchQuery = "a" // matches multiple channels
	m.updateSearchMatches()
	require.True(t, len(m.searchMatches) > 1)

	initial := m.currentMatch
	m.nextMatch()
	assert.Equal(t, initial+1, m.currentMatch)
}

func TestNextMatch_Wraps(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchMatches = []int{0, 1, 2}
	m.currentMatch = 2

	m.nextMatch()
	assert.Equal(t, 0, m.currentMatch)
}

func TestPrevMatch(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchMatches = []int{0, 1, 2}
	m.currentMatch = 1

	m.prevMatch()
	assert.Equal(t, 0, m.currentMatch)
}

func TestPrevMatch_Wraps(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchMatches = []int{0, 1, 2}
	m.currentMatch = 0

	m.prevMatch()
	assert.Equal(t, 2, m.currentMatch)
}

func TestNextMatch_Empty(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchMatches = nil
	m.currentMatch = -1

	// Should not panic
	m.nextMatch()
	assert.Equal(t, -1, m.currentMatch)
}

func TestPrevMatch_Empty(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchMatches = nil
	m.currentMatch = -1

	// Should not panic
	m.prevMatch()
	assert.Equal(t, -1, m.currentMatch)
}

func TestClearSearch(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "test"
	m.searchMatches = []int{0, 1}
	m.currentMatch = 1

	m.clearSearch()

	assert.False(t, m.searching)
	assert.Empty(t, m.searchQuery)
	assert.Nil(t, m.searchMatches)
	assert.Equal(t, -1, m.currentMatch)
}

func TestIsMatch(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchMatches = []int{0, 2}

	assert.True(t, m.isMatch(0))
	assert.False(t, m.isMatch(1))
	assert.True(t, m.isMatch(2))
	assert.False(t, m.isMatch(3))
}

func TestGetPlayingChannel(t *testing.T) {
	m := newTestModel(testChannels())

	// Not playing
	assert.Nil(t, m.getPlayingChannel())

	// Set playing
	m.playingID = "dronezone"
	ch := m.getPlayingChannel()
	require.NotNil(t, ch)
	assert.Equal(t, "Drone Zone", ch.Title)

	// Non-existent ID
	m.playingID = "nonexistent"
	assert.Nil(t, m.getPlayingChannel())
}

// --- Model Update tests ---

func TestUpdate_QuitOnCtrlC(t *testing.T) {
	m := newTestModel(testChannels())

	_, cmd := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	// Should return tea.Quit command
	require.NotNil(t, cmd)
}

func TestUpdate_QuitOnQ(t *testing.T) {
	m := newTestModel(testChannels())

	_, cmd := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'q'}}))
	require.NotNil(t, cmd)
}

func TestUpdate_StopOnS(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "groovesalad"

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'s'}}))
	updated := result.(*model)

	assert.Empty(t, updated.playingID)
	mp := m.player.(*mockPlayer)
	assert.Equal(t, 1, mp.stopCalled)
}

func TestUpdate_ToggleAbout(t *testing.T) {
	m := newTestModel(testChannels())
	assert.False(t, m.showAbout)

	// Press 'a' to show about
	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'a'}}))
	updated := result.(*model)
	assert.True(t, updated.showAbout)

	// Press any key to dismiss
	result, _ = updated.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'x'}}))
	updated = result.(*model)
	assert.False(t, updated.showAbout)
}

func TestUpdate_EnterSearchMode(t *testing.T) {
	m := newTestModel(testChannels())

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'/'}}))
	updated := result.(*model)

	assert.True(t, updated.searching)
	assert.Empty(t, updated.searchQuery)
}

func TestUpdate_SearchInput(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true

	// Type 'd'
	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'d'}}))
	updated := result.(*model)
	assert.Equal(t, "d", updated.searchQuery)

	// Type 'r'
	result, _ = updated.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'r'}}))
	updated = result.(*model)
	assert.Equal(t, "dr", updated.searchQuery)
}

func TestUpdate_SearchBackspace(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "abc"

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyBackspace}))
	updated := result.(*model)
	assert.Equal(t, "ab", updated.searchQuery)
}

func TestUpdate_SearchEscape(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "test"

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyEscape}))
	updated := result.(*model)

	assert.False(t, updated.searching)
	assert.Empty(t, updated.searchQuery)
}

func TestUpdate_SearchEnter(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "groove"

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	updated := result.(*model)

	assert.False(t, updated.searching)
	assert.Equal(t, "groove", updated.searchQuery) // query preserved
}

func TestUpdate_WindowSize(t *testing.T) {
	m := newTestModel(testChannels())

	result, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	updated := result.(*model)

	assert.Equal(t, 120, updated.width)
	assert.Equal(t, 40, updated.height)
}

func TestUpdate_ChannelsLoaded(t *testing.T) {
	m := newTestModel(nil)
	m.loading = true

	channels := testChannels()
	msg := channelsLoadedMsg{
		channels: &Channels{Channels: channels},
	}

	result, _ := m.Update(msg)
	updated := result.(*model)

	assert.False(t, updated.loading)
	assert.Equal(t, len(channels), len(updated.list.Items()))
}

func TestUpdate_ChannelsLoaded_RestoresSelection(t *testing.T) {
	m := newTestModel(nil)
	m.loading = true
	m.state = &State{LastSelectedChannelID: "secretagent"}

	channels := testChannels()
	msg := channelsLoadedMsg{
		channels: &Channels{Channels: channels},
	}

	result, _ := m.Update(msg)
	updated := result.(*model)

	assert.Equal(t, 2, updated.list.Index()) // secretagent is index 2
}

func TestUpdate_ErrorMsg(t *testing.T) {
	m := newTestModel(testChannels())
	m.loading = true

	result, _ := m.Update(errorMsg{err: assert.AnError})
	updated := result.(*model)

	assert.False(t, updated.loading)
	assert.Equal(t, assert.AnError, updated.err)
}

func TestUpdate_TrackUpdate(t *testing.T) {
	m := newTestModel(testChannels())

	result, _ := m.Update(trackUpdateMsg{trackInfo: TrackInfo{Title: "Cool Song"}})
	updated := result.(*model)

	require.NotNil(t, updated.trackInfo)
	assert.Equal(t, "Cool Song", updated.trackInfo.Title)
}

func TestUpdate_StreamError(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "groovesalad"

	result, _ := m.Update(streamErrorMsg{})
	updated := result.(*model)

	assert.Empty(t, updated.playingID)
}

// --- View tests ---

func TestView_Loading(t *testing.T) {
	m := newTestModel(nil)
	m.loading = true

	view := m.View()
	assert.Contains(t, view, "Loading")
}

func TestView_Error(t *testing.T) {
	m := newTestModel(nil)
	m.err = assert.AnError

	view := m.View()
	assert.Contains(t, view, "Error")
}

// --- Smoke / integration test ---

func TestSmoke_FullLifecycle(t *testing.T) {
	setStateDir(t)
	m := newTestModel(testChannels())

	// 1. Simulate channels being loaded
	channels := testChannels()
	m.loading = true
	result, _ := m.Update(channelsLoadedMsg{channels: &Channels{Channels: channels}})
	m = result.(*model)
	assert.False(t, m.loading)
	assert.Equal(t, 3, len(m.list.Items()))

	// 2. Simulate window resize
	result, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = result.(*model)
	assert.Equal(t, 100, m.width)

	// 3. Enter search mode, type a query, confirm
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'/'}}))
	m = result.(*model)
	assert.True(t, m.searching)

	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'s'}}))
	m = result.(*model)
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'a'}}))
	m = result.(*model)
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'l'}}))
	m = result.(*model)
	assert.Equal(t, "sal", m.searchQuery)
	assert.Len(t, m.searchMatches, 1) // "Groove Salad"

	// Confirm search
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyEnter}))
	m = result.(*model)
	assert.False(t, m.searching)
	assert.Equal(t, "sal", m.searchQuery) // preserved

	// 4. Clear search
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'c'}}))
	m = result.(*model)
	assert.Empty(t, m.searchQuery)

	// 5. Stop (nothing playing, should not panic)
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'s'}}))
	m = result.(*model)
	assert.Empty(t, m.playingID)

	// 6. Toggle about screen
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'a'}}))
	m = result.(*model)
	assert.True(t, m.showAbout)

	view := m.View()
	assert.Contains(t, view, "SomaTUI")

	// Dismiss
	result, _ = m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{' '}}))
	m = result.(*model)
	assert.False(t, m.showAbout)

	// 7. Receive track update
	result, _ = m.Update(trackUpdateMsg{trackInfo: TrackInfo{Title: "Test Track"}})
	m = result.(*model)
	require.NotNil(t, m.trackInfo)
	assert.Equal(t, "Test Track", m.trackInfo.Title)

	// 8. View renders without panic
	view = m.View()
	assert.NotEmpty(t, view)
}

// --- PLS/stream test helpers ---

// newPLSServer creates an httptest server that serves a PLS playlist pointing to streamURL.
func newPLSServer(streamURL string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "[playlist]\nNumberOfEntries=1\nFile1=%s\nVersion=2\n", streamURL)
	}))
}

// newFakeStreamServer creates an httptest server that returns 404 (for MetadataReader to fail fast).
func newFakeStreamServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
}

// newTestModelWithPLS creates a model whose channels have playlist URLs pointing to plsServerURL.
func newTestModelWithPLS(plsServerURL string) *model {
	channels := []Channel{
		{
			ID: "ch1", Title: "Channel One", Description: "First channel",
			Listeners: "100",
			Playlists: []Playlist{{URL: plsServerURL, Format: "mp3", Quality: "high"}},
		},
		{
			ID: "ch2", Title: "Channel Two", Description: "Second channel",
			Listeners: "200",
			Playlists: []Playlist{{URL: plsServerURL, Format: "mp3", Quality: "high"}},
		},
		{
			ID: "ch3", Title: "Channel Three", Description: "Third channel",
			Listeners: "300",
			Playlists: []Playlist{{URL: plsServerURL, Format: "mp3", Quality: "high"}},
		},
	}
	return newTestModel(channels)
}

// --- playChannel tests ---

func TestPlayChannel(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	i := item{channel: m.list.Items()[0].(item).channel}

	cmd := m.playChannel(i)

	assert.Equal(t, "ch1", m.playingID)
	mp := m.player.(*mockPlayer)
	assert.Equal(t, 1, mp.playCalled)
	assert.Equal(t, streamServer.URL, mp.playURL)
	assert.NotNil(t, cmd)
	assert.NotNil(t, m.metadataReader)

	// Cleanup
	m.stopMetadataReader()
}

func TestPlayChannel_NoMP3Playlist(t *testing.T) {
	m := newTestModel([]Channel{
		{ID: "aac-only", Title: "AAC Only", Playlists: []Playlist{
			{URL: "http://example.com", Format: "aac"},
		}},
	})

	i := item{channel: m.list.Items()[0].(item).channel}
	cmd := m.playChannel(i)

	assert.Equal(t, "aac-only", m.playingID) // playingID is set before playlist check
	assert.Nil(t, cmd)
}

func TestPlayChannel_PlaylistFetchError(t *testing.T) {
	m := newTestModel([]Channel{
		{ID: "bad-pls", Title: "Bad PLS", Playlists: []Playlist{
			{URL: "http://127.0.0.1:1/nonexistent.pls", Format: "mp3"},
		}},
	})

	i := item{channel: m.list.Items()[0].(item).channel}
	cmd := m.playChannel(i)

	assert.Nil(t, cmd) // should return nil on playlist fetch error
}

func TestPlayChannel_PlayerError(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	mp := m.player.(*mockPlayer)
	mp.playErr = errors.New("audio device unavailable")

	i := item{channel: m.list.Items()[0].(item).channel}
	cmd := m.playChannel(i)

	assert.Nil(t, cmd) // should return nil on player error
	assert.Equal(t, 1, mp.playCalled)
}

// --- MPRIS message handler tests ---

func TestUpdate_MprisStop(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "groovesalad"

	result, _ := m.Update(mprisStopMsg{})
	updated := result.(*model)

	assert.Empty(t, updated.playingID)
	mp := m.player.(*mockPlayer)
	assert.Equal(t, 1, mp.stopCalled)
}

func TestUpdate_MprisStop_NotPlaying(t *testing.T) {
	m := newTestModel(testChannels())

	result, _ := m.Update(mprisStopMsg{})
	updated := result.(*model)

	assert.Empty(t, updated.playingID)
	mp := m.player.(*mockPlayer)
	assert.Equal(t, 0, mp.stopCalled) // should not call Stop when not playing
}

func TestUpdate_MprisPlayPause_StopsWhenPlaying(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "dronezone"

	result, _ := m.Update(mprisPlayPauseMsg{})
	updated := result.(*model)

	assert.Empty(t, updated.playingID)
	mp := m.player.(*mockPlayer)
	assert.Equal(t, 1, mp.stopCalled)
}

func TestUpdate_MprisNext(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	m.list.Select(0) // Start at channel 0

	result, cmd := m.Update(mprisNextMsg{})
	updated := result.(*model)

	assert.Equal(t, 1, updated.list.Index())  // moved to next
	assert.Equal(t, "ch2", updated.playingID) // playing the next channel
	assert.NotNil(t, cmd)

	updated.stopMetadataReader()
}

func TestUpdate_MprisNext_Wraps(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	m.list.Select(2) // Start at last channel

	result, cmd := m.Update(mprisNextMsg{})
	updated := result.(*model)

	assert.Equal(t, 0, updated.list.Index())  // wrapped to first
	assert.Equal(t, "ch1", updated.playingID)
	assert.NotNil(t, cmd)

	updated.stopMetadataReader()
}

func TestUpdate_MprisPrev(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	m.list.Select(1) // Start at channel 1

	result, cmd := m.Update(mprisPrevMsg{})
	updated := result.(*model)

	assert.Equal(t, 0, updated.list.Index())  // moved to previous
	assert.Equal(t, "ch1", updated.playingID)
	assert.NotNil(t, cmd)

	updated.stopMetadataReader()
}

func TestUpdate_MprisPrev_Wraps(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	m.list.Select(0) // Start at first channel

	result, cmd := m.Update(mprisPrevMsg{})
	updated := result.(*model)

	assert.Equal(t, 2, updated.list.Index())  // wrapped to last
	assert.Equal(t, "ch3", updated.playingID)
	assert.NotNil(t, cmd)

	updated.stopMetadataReader()
}

func TestUpdate_MprisPlay_WhenStopped(t *testing.T) {
	setStateDir(t)

	streamServer := newFakeStreamServer()
	defer streamServer.Close()
	plsServer := newPLSServer(streamServer.URL)
	defer plsServer.Close()

	m := newTestModelWithPLS(plsServer.URL)
	m.list.Select(0)

	result, cmd := m.Update(mprisPlayMsg{})
	updated := result.(*model)

	assert.Equal(t, "ch1", updated.playingID)
	assert.NotNil(t, cmd)

	updated.stopMetadataReader()
}

func TestUpdate_MprisPlay_AlreadyPlaying(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "groovesalad" // already playing

	result, _ := m.Update(mprisPlayMsg{})
	updated := result.(*model)

	// Should not change anything
	assert.Equal(t, "groovesalad", updated.playingID)
}

// --- channelsRefreshedMsg tests ---

func TestUpdate_ChannelsRefreshed(t *testing.T) {
	m := newTestModel(testChannels())
	m.list.Select(1) // Select Drone Zone

	// Refresh with updated data
	newChannels := []Channel{
		{ID: "groovesalad", Title: "Groove Salad Updated", Listeners: "2000"},
		{ID: "dronezone", Title: "Drone Zone Updated", Listeners: "600"},
		{ID: "secretagent", Title: "Secret Agent Updated", Listeners: "800"},
	}

	result, _ := m.Update(channelsRefreshedMsg{channels: &Channels{Channels: newChannels}})
	updated := result.(*model)

	assert.Equal(t, 3, len(updated.list.Items()))
	assert.Equal(t, 1, updated.list.Index()) // selection preserved

	// Verify updated data
	first := updated.list.Items()[0].(item)
	assert.Equal(t, "Groove Salad Updated", first.channel.Title)
}

func TestUpdate_ChannelsRefreshed_SelectionBeyondRange(t *testing.T) {
	m := newTestModel(testChannels())
	m.list.Select(2) // Select last item

	// Refresh with fewer channels
	newChannels := []Channel{
		{ID: "groovesalad", Title: "Groove Salad"},
	}

	result, _ := m.Update(channelsRefreshedMsg{channels: &Channels{Channels: newChannels}})
	updated := result.(*model)

	assert.Equal(t, 1, len(updated.list.Items()))
	// Selection should not crash even though original index was 2
}

func TestUpdate_ChannelsLoaded_FromCache_TriggersRefresh(t *testing.T) {
	m := newTestModel(nil)
	m.loading = true

	msg := channelsLoadedMsg{
		channels: &Channels{Channels: testChannels()},
		fromCache: true,
	}

	_, cmd := m.Update(msg)

	// When loaded from cache, should return a command to refresh from network
	assert.NotNil(t, cmd)
}

// --- Search navigation key tests ---

func TestUpdate_SearchNextN(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchQuery = "a"
	m.updateSearchMatches()
	require.True(t, len(m.searchMatches) > 1)
	initial := m.currentMatch

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))
	updated := result.(*model)

	assert.Equal(t, initial+1, updated.currentMatch)
}

func TestUpdate_SearchPrevN(t *testing.T) {
	m := newTestModel(testChannels())
	m.searchQuery = "a"
	m.updateSearchMatches()
	require.True(t, len(m.searchMatches) > 1)
	m.currentMatch = 1

	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'N'}}))
	updated := result.(*model)

	assert.Equal(t, 0, updated.currentMatch)
}

func TestUpdate_SearchN_NoMatches(t *testing.T) {
	m := newTestModel(testChannels())
	// No search active, n should not crash
	result, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune{'n'}}))
	assert.NotNil(t, result)
}

// --- About screen Ctrl+C test ---

func TestUpdate_AboutScreen_CtrlC(t *testing.T) {
	m := newTestModel(testChannels())
	m.showAbout = true

	_, cmd := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	// Should quit even from about screen
	assert.NotNil(t, cmd)
}

// --- Search mode Ctrl+C test ---

func TestUpdate_SearchMode_CtrlC(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true

	_, cmd := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyCtrlC}))
	// Should quit even from search mode
	assert.NotNil(t, cmd)
}

// --- Render tests ---

func TestRenderStatusBar_Stopped(t *testing.T) {
	m := newTestModel(testChannels())
	bar := m.renderStatusBar()
	assert.Contains(t, bar, "Stopped")
}

func TestRenderStatusBar_Playing(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "groovesalad"

	bar := m.renderStatusBar()
	assert.Contains(t, bar, "Playing")
	assert.Contains(t, bar, "Groove Salad")
}

func TestRenderStatusBar_PlayingWithTrack(t *testing.T) {
	m := newTestModel(testChannels())
	m.playingID = "groovesalad"
	m.trackInfo = &TrackInfo{Title: "Cool Song - Artist"}

	bar := m.renderStatusBar()
	assert.Contains(t, bar, "Playing")
	assert.Contains(t, bar, "Groove Salad")
	assert.Contains(t, bar, "Cool Song - Artist")
}

func TestRenderSearchBar_Active(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "test"

	bar := m.renderSearchBar()
	assert.Contains(t, bar, "/test")
}

func TestRenderSearchBar_ActiveWithMatches(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "zone"
	m.updateSearchMatches()

	bar := m.renderSearchBar()
	assert.Contains(t, bar, "/zone")
	assert.Contains(t, bar, "1/1") // one match
}

func TestRenderSearchBar_ActiveNoMatches(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = true
	m.searchQuery = "zzzzz"
	m.updateSearchMatches()

	bar := m.renderSearchBar()
	assert.Contains(t, bar, "no matches")
}

func TestRenderSearchBar_InactiveWithQuery(t *testing.T) {
	m := newTestModel(testChannels())
	m.searching = false
	m.searchQuery = "zone"
	m.searchMatches = []int{1}
	m.currentMatch = 0

	bar := m.renderSearchBar()
	assert.Contains(t, bar, "Search: zone")
	assert.Contains(t, bar, "1/1")
}

func TestRenderSearchBar_Empty(t *testing.T) {
	m := newTestModel(testChannels())
	bar := m.renderSearchBar()
	assert.Empty(t, bar)
}

func TestRenderHeader(t *testing.T) {
	m := newTestModel(testChannels())
	header := m.renderHeader()
	assert.Contains(t, header, "SomaFM Stations")
	assert.Contains(t, header, "Listeners")
}

// --- stopMetadataReader tests ---

func TestStopMetadataReader_Nil(t *testing.T) {
	m := newTestModel(testChannels())
	m.metadataReader = nil
	// Should not panic
	m.stopMetadataReader()
}

func TestStopMetadataReader_Active(t *testing.T) {
	m := newTestModel(testChannels())
	m.metadataReader = NewMetadataReader("http://example.com/stream")
	m.stopMetadataReader()
	assert.Nil(t, m.metadataReader)
}

// --- updateMPRIS tests ---

func TestUpdateMPRIS_NilMPRIS(t *testing.T) {
	m := newTestModel(testChannels())
	m.mpris = nil
	// Should not panic
	m.updateMPRIS()
}

// --- View with about overlay ---

func TestView_WithAbout(t *testing.T) {
	m := newTestModel(testChannels())
	m.width = 100
	m.height = 30
	m.showAbout = true
	m.about = aboutInfo{Version: "1.0.0", Commit: "abc123", Date: "2025-01-01"}

	view := m.View()
	assert.Contains(t, view, "SomaTUI")
	assert.Contains(t, view, "1.0.0")
}
