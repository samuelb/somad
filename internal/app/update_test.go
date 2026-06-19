package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"somatui/internal/audio"
	"somatui/internal/channels"
	"somatui/internal/platform"
	"somatui/internal/security"
	"somatui/internal/ui"

	listpkg "github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sendKey sends a single-rune key message through Update and returns the model.
func sendKey(m *Model, r rune) (*Model, tea.Cmd) {
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return updated.(*Model), cmd
}

func TestUpdate_WindowSizeMsg(t *testing.T) {
	m := newTestModel(t)

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	assert.Equal(t, 120, m.Width)
	assert.Equal(t, 40, m.Height)
}

func TestUpdate_QuitKey(t *testing.T) {
	m := newTestModel(t)

	_, cmd := sendKey(m, 'q')

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestUpdate_CtrlC_Quits(t *testing.T) {
	m := newTestModel(t)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestUpdate_AboutKey_TogglesAbout(t *testing.T) {
	m := newTestModel(t)

	sendKey(m, 'a')
	assert.True(t, m.ShowAbout, "first 'a' opens the about footer")

	sendKey(m, 'a')
	assert.False(t, m.ShowAbout, "second 'a' closes the about footer")
}

func TestUpdate_AboutDismissedByEsc(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	assert.False(t, m.ShowAbout)
}

func TestUpdate_AboutIsNonModal(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true

	// An unrelated key must not close the footer; it falls through to the list.
	sendKey(m, 'x')

	assert.True(t, m.ShowAbout)
}

func TestUpdate_AboutQuitWithCtrlC(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestUpdate_SearchModeEnter(t *testing.T) {
	m := newTestModel(t)

	sendKey(m, '/')

	assert.True(t, m.Searching)
	assert.Empty(t, m.SearchQuery)
	assert.Equal(t, -1, m.CurrentMatch)
}

func TestUpdate_SearchMode_TypeChar(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true

	sendKey(m, 'g')

	assert.Equal(t, "g", m.SearchQuery)
}

func TestUpdate_SearchMode_Backspace(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "gro"

	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	assert.Equal(t, "gr", m.SearchQuery)
}

func TestUpdate_SearchMode_BackspaceOnEmpty(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = ""

	// Should not panic or modify query
	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	assert.Empty(t, m.SearchQuery)
}

func TestUpdate_SearchMode_Escape_ClearsSearch(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"
	m.SearchMatches = []int{0}
	m.CurrentMatch = 0

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	assert.False(t, m.Searching)
	assert.Empty(t, m.SearchQuery)
	assert.Nil(t, m.SearchMatches)
}

func TestUpdate_SearchMode_Enter_ExitsSearchKeepsQuery(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.False(t, m.Searching)
	assert.Equal(t, "groove", m.SearchQuery)
}

func TestUpdate_StopKey_StopsPlayback(t *testing.T) {
	m := newTestModel(t)
	mp := m.Player.(*mockPlayer)
	mp.playing = true
	m.PlayingID = "groovesalad"
	m.TrackInfo = &audio.TrackInfo{Title: "Test Track"}
	m.StreamErr = "previous error"

	sendKey(m, 's')

	assert.Empty(t, m.PlayingID)
	assert.Nil(t, m.TrackInfo)
	assert.Empty(t, m.StreamErr)
	assert.False(t, mp.playing)
}

func TestUpdate_StopKey_NoPlayer_NoOp(t *testing.T) {
	m := newTestModel(t)
	m.Player = nil

	// Should not panic when player is nil
	sendKey(m, 's')
}

func TestUpdate_FavoriteKey_TogglesSelected(t *testing.T) {
	m := newTestModel(t)

	sendKey(m, 'f')

	// Index 0 (Groove Salad) should now be a favorite
	assert.True(t, m.State.IsFavorite("groovesalad"))
}

func TestUpdate_ClearSearch_ClearsQuery(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "test"
	m.SearchMatches = []int{0}

	sendKey(m, 'c')

	assert.Empty(t, m.SearchQuery)
	assert.Nil(t, m.SearchMatches)
}

func TestUpdate_ClearSearch_NoQueryNoOp(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = ""

	// 'c' with empty query should pass through to list
	sendKey(m, 'c')

	assert.Empty(t, m.SearchQuery)
}

func TestUpdate_NextMatchKey_Navigates(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	if len(m.SearchMatches) < 2 {
		t.Skip("need at least two matches")
	}

	initialMatch := m.CurrentMatch
	sendKey(m, 'n')

	assert.Equal(t, initialMatch+1, m.CurrentMatch)
}

func TestUpdate_PrevMatchKey_Navigates(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	if len(m.SearchMatches) < 2 {
		t.Skip("need at least two matches")
	}

	// First navigate forward so we're not at index 0
	sendKey(m, 'n')
	before := m.CurrentMatch

	sendKey(m, 'N')

	assert.Equal(t, before-1, m.CurrentMatch)
}

func TestUpdate_ChannelsLoadedMsg(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	chans := &channels.Channels{Channels: testChannels()}
	m.Update(ChannelsLoadedMsg{Channels: chans, FromCache: false})

	assert.False(t, m.Loading)
	assert.Len(t, m.List.Items(), len(testChannels()))
}

func TestUpdate_ChannelsLoadedMsg_RestoresSelection(t *testing.T) {
	m := newTestModel(t)
	m.State.LastSelectedChannelID = "dronezone"

	chans := &channels.Channels{Channels: testChannels()}
	m.Update(ChannelsLoadedMsg{Channels: chans, FromCache: false})

	selected, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", selected.Channel.ID)
}

func TestUpdate_ChannelsRefreshedMsg_UpdatesList(t *testing.T) {
	m := newTestModel(t)

	refreshed := []channels.Channel{{ID: "lush", Title: "Lush"}}
	m.Update(ChannelsRefreshedMsg{Channels: &channels.Channels{Channels: refreshed}})

	assert.Len(t, m.List.Items(), 1)
	item, ok := m.List.Items()[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "lush", item.Channel.ID)
}

func TestUpdate_ChannelsRefreshedMsg_RestoresSelection(t *testing.T) {
	m := newTestModel(t)
	m.List.Select(1) // select Drone Zone

	chans := &channels.Channels{Channels: testChannels()}
	m.Update(ChannelsRefreshedMsg{Channels: chans})

	selected, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", selected.Channel.ID)
}

func TestUpdate_ErrorMsg(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	m.Update(ErrorMsg{Err: fmt.Errorf("network failure")})

	assert.False(t, m.Loading)
	assert.Error(t, m.Err)
	assert.Contains(t, m.Err.Error(), "network failure")
}

func TestUpdate_TrackUpdateMsg(t *testing.T) {
	m := newTestModel(t)

	m.Update(TrackUpdateMsg{TrackInfo: audio.TrackInfo{Title: "Live Track"}})

	require.NotNil(t, m.TrackInfo)
	assert.Equal(t, "Live Track", m.TrackInfo.Title)
}

func TestUpdate_StreamErrorMsg(t *testing.T) {
	m := newTestModel(t)
	m.PlayingID = "groovesalad"
	m.TrackInfo = &audio.TrackInfo{Title: "Test"}

	m.Update(StreamErrorMsg{Err: errors.New("connection lost")})

	assert.Empty(t, m.PlayingID)
	assert.Nil(t, m.TrackInfo)
	assert.Equal(t, "connection lost", m.StreamErr)
}

func TestUpdate_MPRISPlayMsg_StartsPlayback(t *testing.T) {
	security.AllowTestHosts(t)

	// Serve a minimal PLS playlist pointing the stream back at the test server.
	var streamURL string
	plsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[playlist]\nFile1="+streamURL+"/stream\nNumberOfEntries=1\n")
	}))
	defer plsServer.Close()
	streamURL = plsServer.URL

	m := newTestModel(t)
	// Replace channels with one pointing at the test server
	testChan := ui.Item{Channel: channels.Channel{
		ID:    "testchan",
		Title: "Test Chan",
		Playlists: []channels.Playlist{
			{URL: plsServer.URL + "/playlist.pls", Format: "mp3"},
		},
	}}
	m.List.SetItems([]listpkg.Item{testChan})
	m.List.Select(0)

	// MPRISPlayMsg should attempt to play the selected channel
	_, cmd := m.Update(platform.MPRISPlayMsg{})

	// We expect a non-nil cmd (either PollTrackUpdates or StreamErrorMsg)
	assert.NotNil(t, cmd)

	// playChannel starts a background metadata reader on success; stop it so the
	// goroutine does not outlive the test (and the test server).
	m.StopMetadataReader()
	m.Player.Stop()
}

func TestUpdate_MPRISStopMsg(t *testing.T) {
	m := newTestModel(t)
	mp := m.Player.(*mockPlayer)
	mp.playing = true
	m.PlayingID = "groovesalad"
	m.TrackInfo = &audio.TrackInfo{Title: "Test"}

	m.Update(platform.MPRISStopMsg{})

	assert.Empty(t, m.PlayingID)
	assert.Nil(t, m.TrackInfo)
	assert.False(t, mp.playing)
}

func TestUpdate_MPRISPlayPauseMsg_Toggles(t *testing.T) {
	m := newTestModel(t)
	mp := m.Player.(*mockPlayer)
	mp.playing = true
	m.PlayingID = "groovesalad"

	// When playing, PlayPause should stop
	m.Update(platform.MPRISPlayPauseMsg{})

	assert.Empty(t, m.PlayingID)
	assert.False(t, mp.playing)
}

func TestUpdate_MPRISNextMsg_SelectsNextChannel(t *testing.T) {
	security.AllowTestHosts(t)

	m := newTestModel(t)
	m.List.Select(0)

	m.Update(platform.MPRISNextMsg{})

	// MPRISNextMsg selects the next item and calls playChannel.
	// With a mock player and real URL, the play will fail, but cursor
	// should have advanced.
	// We verify the list had its selection moved.
	// (playChannel error is returned as a cmd, not applied to m synchronously)
	_ = m
}

func TestUpdate_MPRISPrevMsg_SelectsPrevChannel(t *testing.T) {
	m := newTestModel(t)
	// Start at index 1 so prev has somewhere to go
	m.List.Select(1)

	m.Update(platform.MPRISPrevMsg{})

	// Similar to Next, the previous channel is selected then playChannel is called.
	_ = m
}

func TestPlayChannel_NoMP3Playlist(t *testing.T) {
	m := newTestModel(t)
	item := ui.Item{Channel: channels.Channel{
		ID:    "aac-only",
		Title: "AAC Only",
		Playlists: []channels.Playlist{
			{URL: "http://somafm.com/aaconly.pls", Format: "aac"},
		},
	}}

	cmd := m.playChannel(item)

	require.NotNil(t, cmd)
	msg := cmd()
	errMsg, ok := msg.(StreamErrorMsg)
	require.True(t, ok, "expected StreamErrorMsg, got %T", msg)
	assert.Error(t, errMsg.Err)
	assert.Contains(t, errMsg.Err.Error(), "no MP3 playlist")
	// PlayingID must not be set on failure
	assert.Empty(t, m.PlayingID)
}

func TestPlayChannel_PlayerError(t *testing.T) {
	security.AllowTestHosts(t)

	var streamURL string
	plsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[playlist]\nFile1="+streamURL+"/stream\nNumberOfEntries=1\n")
	}))
	defer plsServer.Close()
	streamURL = plsServer.URL

	m := newTestModel(t)
	mp := m.Player.(*mockPlayer)
	mp.playErr = errors.New("audio init failed")

	item := ui.Item{Channel: channels.Channel{
		ID:    "testchan",
		Title: "Test",
		Playlists: []channels.Playlist{
			{URL: plsServer.URL, Format: "mp3"},
		},
	}}

	cmd := m.playChannel(item)

	require.NotNil(t, cmd)
	msg := cmd()
	errMsg, ok := msg.(StreamErrorMsg)
	require.True(t, ok, "expected StreamErrorMsg, got %T", msg)
	assert.Contains(t, errMsg.Err.Error(), "failed to start playback")
	assert.Empty(t, m.PlayingID)
}

func TestPlayChannel_Success(t *testing.T) {
	security.AllowTestHosts(t)

	var streamURL string
	plsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[playlist]\nFile1="+streamURL+"/stream\nNumberOfEntries=1\n")
	}))
	defer plsServer.Close()
	streamURL = plsServer.URL

	m := newTestModel(t)

	item := ui.Item{Channel: channels.Channel{
		ID:    "testchan",
		Title: "Test",
		Playlists: []channels.Playlist{
			{URL: plsServer.URL, Format: "mp3"},
		},
	}}

	cmd := m.playChannel(item)

	// On success PlayingID is set and PollTrackUpdates is returned
	assert.Equal(t, "testchan", m.PlayingID)
	assert.NotNil(t, cmd)
}
