package app

import (
	"errors"
	"testing"

	"somatui/internal/protocol"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
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

func TestUpdate_QuitKey_DoesNotStopPlayback(t *testing.T) {
	m := newTestModel(t)

	_, cmd := sendKey(m, 'q')

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	// Quitting the TUI must leave the server playing.
	assert.Zero(t, backend(m).stops)
	assert.Zero(t, backend(m).shutdowns)
}

func TestUpdate_CtrlC_Quits(t *testing.T) {
	m := newTestModel(t)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.Zero(t, backend(m).stops)
	assert.Zero(t, backend(m).shutdowns)
}

func TestUpdate_QuitKey_ShutsDownServerWhenConfigured(t *testing.T) {
	m := newTestModel(t)
	var exited bool
	m.ShutdownOnExit = true
	m.OnExit = func() { exited = true }

	_, cmd := sendKey(m, 'q')

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
	assert.True(t, exited)
	assert.Equal(t, 1, backend(m).shutdowns)
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

func TestUpdate_SearchMode_TypeNonASCII(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true

	sendKey(m, 'ü')

	assert.Equal(t, "ü", m.SearchQuery)
}

func TestUpdate_SearchMode_TypeSpace(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"

	m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})

	assert.Equal(t, "groove ", m.SearchQuery)
}

func TestUpdate_SearchMode_BackspaceDeletesFullRune(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "aü"

	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	assert.Equal(t, "a", m.SearchQuery)
}

func TestUpdate_SearchMode_BackspaceOnEmpty(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = ""

	m.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	assert.Empty(t, m.SearchQuery)
}

func TestUpdate_SearchMode_Escape_ClearsSearch(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"
	m.UpdateSearchMatches()

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	assert.False(t, m.Searching)
	assert.Empty(t, m.SearchQuery)
	assert.Empty(t, m.SearchMatches)
}

func TestUpdate_SearchMode_Enter_ExitsSearchKeepsQuery(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.False(t, m.Searching)
	assert.Equal(t, "groove", m.SearchQuery)
}

func TestUpdate_SearchMode_CtrlC_Quits(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	require.NotNil(t, cmd)
	assert.IsType(t, tea.QuitMsg{}, cmd())
}

func TestUpdate_PlayKey_PlaysSelectedChannel(t *testing.T) {
	m := newTestModel(t)
	m.List.Select(1) // dronezone

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	msg := runCmd(cmd)
	assert.Equal(t, []string{"dronezone"}, backend(m).playIDs)

	// The returned snapshot applies immediately.
	m.Update(msg)
	assert.Equal(t, protocol.StatusPlaying, m.Snapshot.Status)
	assert.Equal(t, "dronezone", m.PlayingID)
}

func TestUpdate_StopKey_StopsPlayback(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad", Volume: 1})

	_, cmd := sendKey(m, 's')

	msg := runCmd(cmd)
	assert.Equal(t, 1, backend(m).stops)

	m.Update(msg)
	assert.Equal(t, protocol.StatusStopped, m.Snapshot.Status)
	assert.Empty(t, m.PlayingID)
}

func TestUpdate_PlayKey_RestartsSkewedServerThenPlays(t *testing.T) {
	m := newTestModel(t)
	m.About.Version = "new"
	m.ServerVersion = "old" // server is out of date
	m.List.Select(1)        // dronezone

	// Enter must not play on the stale server; it shuts it down for a restart
	// and remembers the channel to play once a fresh backend arrives.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runCmd(cmd)
	assert.Equal(t, 1, backend(m).shutdowns, "a skewed server is restarted, not played on")
	assert.Empty(t, backend(m).playIDs, "nothing is played on the stale server")
	assert.Equal(t, "dronezone", m.pendingPlayID)

	// The reconnect delivers a fresh, up-to-date backend; the pending channel
	// plays on it and the skew clears.
	fresh := newFakeBackend()
	_, cmd = m.Update(ServerReconnectedMsg{Backend: fresh, ServerVersion: "new"})
	for _, c := range runCmd(cmd).(tea.BatchMsg) {
		runCmd(c)
	}
	assert.Equal(t, "new", m.ServerVersion)
	assert.Empty(t, m.pendingPlayID)
	assert.False(t, m.skewed())
	assert.Equal(t, []string{"dronezone"}, fresh.playIDs, "the queued channel plays on the fresh server")
}

func TestUpdate_StopKey_RestartsSkewedServer(t *testing.T) {
	m := newTestModel(t)
	m.About.Version = "new"
	m.ServerVersion = "old"
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad", Volume: 1})

	// Stopping a stale, playing server restarts it (it comes back stopped)
	// rather than issuing Stop on the outgoing binary.
	_, cmd := sendKey(m, 's')
	runCmd(cmd)
	assert.Equal(t, 1, backend(m).shutdowns)
	assert.Zero(t, backend(m).stops, "stop is not sent to the stale server")
	assert.Empty(t, m.pendingPlayID, "stop queues no playback")
}

func TestUpdate_PlayKey_DoesNotRestartWhenVersionsMatch(t *testing.T) {
	m := newTestModel(t)
	m.About.Version = "same"
	m.ServerVersion = "same"
	m.List.Select(1)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	runCmd(cmd)

	// No skew: play goes straight to the current server, no restart.
	assert.Zero(t, backend(m).shutdowns)
	assert.Equal(t, []string{"dronezone"}, backend(m).playIDs)
}

func TestUpdate_VolumeKeys_AdjustVolume(t *testing.T) {
	m := newTestModel(t)
	m.Snapshot.Volume = 0.5

	_, cmd := sendKey(m, '+')
	m.Update(runCmd(cmd))
	assert.InDelta(t, 0.55, m.Snapshot.Volume, 1e-9)

	_, cmd = sendKey(m, '-')
	m.Update(runCmd(cmd))
	assert.InDelta(t, 0.5, m.Snapshot.Volume, 1e-9)
}

func TestUpdate_VolumeKeys_ClampAtBounds(t *testing.T) {
	m := newTestModel(t)
	m.Snapshot.Volume = 0.99

	_, cmd := sendKey(m, '+')
	m.Update(runCmd(cmd))
	assert.InDelta(t, 1.0, m.Snapshot.Volume, 1e-9)

	m.Snapshot.Volume = 0.01
	_, cmd = sendKey(m, '-')
	m.Update(runCmd(cmd))
	assert.InDelta(t, 0.0, m.Snapshot.Volume, 1e-9)
}

func TestUpdate_FavoriteKey_TogglesSelected(t *testing.T) {
	m := newTestModel(t)
	m.List.Select(1) // dronezone

	_, cmd := sendKey(m, 'f')

	// Optimistic local toggle happened immediately.
	assert.Equal(t, []string{"dronezone"}, m.Favorites)
	// And the server was told.
	runCmd(cmd)
	assert.Equal(t, []string{"dronezone"}, backend(m).favorites)
}

func TestUpdate_ClearSearch_ClearsQuery(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "groove"
	m.UpdateSearchMatches()

	sendKey(m, 'c')

	assert.Empty(t, m.SearchQuery)
	assert.Empty(t, m.SearchMatches)
}

func TestUpdate_NextMatchKey_Navigates(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require.Len(t, m.SearchMatches, 2)
	first := m.CurrentMatch

	sendKey(m, 'n')

	assert.NotEqual(t, first, m.CurrentMatch)
}

func TestUpdate_PrevMatchKey_Navigates(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	require.Len(t, m.SearchMatches, 2)

	sendKey(m, 'n')
	sendKey(m, 'N')

	assert.Equal(t, 0, m.CurrentMatch)
}

func TestUpdate_ServerStateMsg_AppliesSnapshot(t *testing.T) {
	m := newTestModel(t)

	m.Update(ServerStateMsg{State: protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 0.7,
	}})

	assert.Equal(t, protocol.StatusPlaying, m.Snapshot.Status)
	assert.Equal(t, "groovesalad", m.PlayingID)

	m.Update(ServerStateMsg{State: protocol.PlaybackState{Status: protocol.StatusConnecting, ChannelID: "dronezone", Volume: 0.7}})
	assert.Empty(t, m.PlayingID, "the playing marker only shows for actually playing channels")
}

func TestUpdate_ServerChannelsMsg_LoadsCatalog(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true
	m.List.SetItems(nil)

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{
		Channels:      testChannels(),
		LastChannelID: "dronezone",
	}})

	assert.False(t, m.Loading)
	assert.Len(t, m.List.Items(), 3)
	// The last-played channel is selected on first load.
	sel, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", sel.Channel.ID)
}

func TestUpdate_ServerChannelsMsg_SortsFavoritesFirst(t *testing.T) {
	m := newTestModel(t)

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{
		Channels:  testChannels(),
		Favorites: []string{"secretagent"},
	}})

	first, ok := m.List.Items()[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "secretagent", first.Channel.ID)
}

func TestUpdate_ServerChannelsMsg_KeepsSelection(t *testing.T) {
	m := newTestModel(t)
	m.Loading = false
	m.List.Select(2) // secretagent

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{
		Channels:  testChannels(),
		Favorites: []string{"secretagent"},
	}})

	sel, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "secretagent", sel.Channel.ID)
}

func TestUpdate_ServerChannelsMsg_EmptyWithErrorShowsError(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Error: "fetch failed"}})

	assert.False(t, m.Loading)
	require.Error(t, m.Err)
	assert.Contains(t, m.Err.Error(), "fetch failed")
}

func TestUpdate_ServerChannelsMsg_EmptyWithoutErrorKeepsLoading(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{}})

	assert.True(t, m.Loading, "an empty payload without error means the load is still underway")
	assert.NoError(t, m.Err)
}

func TestUpdate_ServerChannelsMsg_RecoversFromError(t *testing.T) {
	m := newTestModel(t)
	m.Err = errors.New("earlier failure")

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: testChannels()}})

	assert.NoError(t, m.Err)
}

func TestUpdate_ServerLostAndReconnected(t *testing.T) {
	m := newTestModel(t)

	m.Update(ServerLostMsg{})
	assert.True(t, m.ServerLost)

	fresh := newFakeBackend()
	_, cmd := m.Update(ServerReconnectedMsg{Backend: fresh})

	assert.False(t, m.ServerLost)
	assert.Same(t, fresh, m.Backend)
	assert.NotNil(t, cmd, "reconnect refetches channels and status")
}

func TestUpdate_ServerGoneMsg_ShowsError(t *testing.T) {
	m := newTestModel(t)
	m.ServerLost = true

	m.Update(ServerGoneMsg{Err: errors.New("gave up")})

	assert.False(t, m.ServerLost)
	require.Error(t, m.Err)
}

// Mirrors cmd/somatui main.go construction: empty list created at 0x0, then
// channels arrive, then a window size, then enter plays the selection.
func TestUpdate_EnterPlaysAfterStartupFlow(t *testing.T) {
	m := &Model{
		Backend:      newFakeBackend(),
		Loading:      true,
		CurrentMatch: -1,
	}
	delegate := ui.NewStyledDelegate(&m.PlayingID, m.IsMatch, m.IsFavorite)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	m.List = l

	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{
		Channels:      testChannels(),
		LastChannelID: "groovesalad",
	}})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, cmd, "enter should produce a play command")
	runCmd(cmd)
	assert.Equal(t, []string{"groovesalad"}, backend(m).playIDs)
}

func TestInit_FetchesChannelsAndStatus(t *testing.T) {
	m := newTestModel(t)
	b := backend(m)
	b.mu.Lock()
	b.payload = protocol.ChannelsPayload{Channels: testChannels()}
	b.mu.Unlock()

	cmd := m.Init()
	require.NotNil(t, cmd)
}
