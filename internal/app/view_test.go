package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSearchBar_Active(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"
	m.UpdateSearchMatches()

	result := m.RenderSearchBar()

	assert.Contains(t, result, "groove")
	assert.Contains(t, result, "1 of 1")
}

func TestRenderSearchBar_ActiveNoMatches(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "xyzzy"
	m.UpdateSearchMatches()

	result := m.RenderSearchBar()

	assert.Contains(t, result, "xyzzy")
	assert.Contains(t, result, "no matches")
}

func TestRenderSearchBar_InactiveWithQuery(t *testing.T) {
	m := newTestModel(t)
	m.Searching = false
	m.SearchQuery = "groove"
	m.UpdateSearchMatches()

	result := m.RenderSearchBar()

	assert.Contains(t, result, "groove")
	assert.Contains(t, result, "1 of 1")
	assert.Contains(t, result, "n/N navigate")
}

func TestRenderSearchBar_InactiveNoQuery(t *testing.T) {
	m := newTestModel(t)
	m.Searching = false
	m.SearchQuery = ""

	result := m.RenderSearchBar()

	assert.Empty(t, result)
}

func TestRenderNowPlaying_Stopped(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "Stopped")
	assert.Contains(t, result, "■")
}

func TestRenderNowPlaying_Connecting(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusConnecting, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
	})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "Connecting")
	assert.Contains(t, result, ui.Spinner(0))
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderNowPlaying_ShowsVolume(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 0.85})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "vol")
	assert.Contains(t, result, "85%")
}

func TestRenderNowPlaying_Reconnecting(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusReconnecting, ChannelID: "groovesalad", ChannelTitle: "Groove Salad",
		ReconnectAttempt: 2, Volume: 1,
	})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "Reconnecting #2")
	assert.Contains(t, result, ui.Spinner(0))
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderNowPlaying_Playing(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
	})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "Playing")
	assert.Contains(t, result, "▶")
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderNowPlaying_WithTrackInfo(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad",
		TrackTitle: "Artist - Song", Volume: 1,
	})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "Artist — Song", "artist and title split like MPRIS and Last.fm do")
	assert.Contains(t, result, ui.Equalizer(0))
}

func TestRenderNowPlaying_WithStreamError(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, StreamError: "connection reset", Volume: 1})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "connection reset")
	assert.Contains(t, result, "Stream error")
}

func TestRenderNowPlaying_WrapsOnNarrowTerminals(t *testing.T) {
	m := newTestModel(t)
	m.Width = 30
	m.applySnapshot(protocol.PlaybackState{
		Status:      protocol.StatusStopped,
		StreamError: "stream stalled: no data received for thirty long seconds",
		Volume:      1,
	})

	result := m.RenderNowPlaying()

	// The renderer truncates overlong lines, so an unwrapped bar would clip
	// exactly the error it exists to show; wrapping keeps the tail visible.
	assert.Greater(t, lipgloss.Height(result), 2, "long content must wrap, not overflow one line")
	assert.Contains(t, result, "seconds", "the end of the error must survive wrapping")
	for _, line := range strings.Split(result, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 30, "no line may exceed the terminal width")
	}
}

func TestFormatSleepRemaining(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "minutes", d: 42 * time.Minute, want: "sleep in 42m"},
		{name: "rounds to nearest minute", d: 41*time.Minute + 40*time.Second, want: "sleep in 42m"},
		{name: "under a minute shows seconds", d: 30 * time.Second, want: "sleep in 30s"},
		{name: "exactly a minute", d: time.Minute, want: "sleep in 1m"},
		{name: "negative clamps to zero", d: -5 * time.Second, want: "sleep in 0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatSleepRemaining(tt.d))
		})
	}
}

func TestSleepTickDelay(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want time.Duration // before sleepTickSlack
	}{
		{name: "on a whole minute", d: 42 * time.Minute, want: 30 * time.Second},
		{name: "rounded up", d: 41*time.Minute + 40*time.Second, want: 10 * time.Second},
		{name: "rounded down", d: 41*time.Minute + 20*time.Second, want: 50 * time.Second},
		{name: "last minute shown", d: time.Minute + 10*time.Second, want: 10 * time.Second},
		{name: "seconds", d: 30 * time.Second, want: 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sleepTickDelay(tt.d)
			require.True(t, ok)
			assert.Equal(t, tt.want+sleepTickSlack, got)
		})
	}

	for _, d := range []time.Duration{400 * time.Millisecond, 0, -time.Second} {
		_, ok := sleepTickDelay(d)
		assert.False(t, ok, "%v: the label reads 0s and cannot change again", d)
	}
}

func TestSleepTickDelay_LandsJustAfterEachLabelChange(t *testing.T) {
	for d := time.Duration(0); d < 2*time.Hour; d += 7300 * time.Millisecond {
		delay, ok := sleepTickDelay(d)
		if !ok {
			continue
		}
		label := formatSleepRemaining(d)
		assert.NotEqual(t, label, formatSleepRemaining(d-delay), "%v: the label has changed by the tick", d)
		assert.Equal(t, label, formatSleepRemaining(d-delay+2*sleepTickSlack), "%v: but not long before it", d)
	}
}

func TestSleepTimerLabel_EmptyWhenNotSet(t *testing.T) {
	assert.Empty(t, sleepTimerLabel(""))
}

func TestSleepTimerLabel_EmptyOnMalformedTimestamp(t *testing.T) {
	assert.Empty(t, sleepTimerLabel("not-a-timestamp"))
}

func TestRenderNowPlaying_ShowsSleepTimer(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
		StopAt: time.Now().Add(42 * time.Minute).Format(time.RFC3339),
	})

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "sleep in 42m")
}

func TestRenderNowPlaying_NoSleepTimerByDefault(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})

	result := m.RenderNowPlaying()

	assert.NotContains(t, result, "sleep in")
}

func TestRenderNowPlaying_ServerLost(t *testing.T) {
	m := newTestModel(t)
	m.ServerLost = true

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "server connection lost")
}

func TestRenderHeader_ContainsTitles(t *testing.T) {
	m := newTestModel(t)

	result := m.RenderHeader()

	assert.Contains(t, result, "SomaFM")
	assert.Contains(t, result, "Stations · 3", "the channel count")
	assert.Contains(t, result, "Listeners")
}

func TestView_Loading(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	result := m.View()

	assert.Contains(t, result, "Loading")
}

func TestView_Error(t *testing.T) {
	m := newTestModel(t)
	m.Err = assert.AnError

	result := m.View()

	assert.Contains(t, result, "Error")
	assert.Contains(t, result, "quit")
}

func TestView_NormalContainsChannels(t *testing.T) {
	m := newTestModel(t)
	m.Loading = false
	m.Width = 80
	m.Height = 24

	result := m.View()

	// The main view should include channel names from the list
	assert.NotEmpty(t, result)
	assert.NotContains(t, result, "Loading")
}

func TestView_AboutFooter(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true
	m.Width = 80
	m.Height = 24
	m.About = AboutInfo{Version: "1.2.3", Commit: "abc123", Date: "2024-01-01"}

	result := m.View()

	assert.Contains(t, result, "Soma")
	assert.Contains(t, result, "1.2.3")
	assert.Contains(t, result, "close")
}

func TestRenderHistory_Hidden(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = false

	assert.Empty(t, m.RenderHistory())
}

func TestRenderHistory_NothingPlaying(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = ""

	result := m.RenderHistory()

	assert.Contains(t, result, "Nothing is playing")
}

func TestRenderHistory_ContainsEntries(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.History = []protocol.HistoryEntry{
		{Title: "Artist - First Track"},
		{Title: "Artist - Second Track"},
	}

	result := m.RenderHistory()

	assert.Contains(t, result, "Groove Salad")
	assert.Contains(t, result, "Artist — First Track")
	assert.Contains(t, result, "Artist — Second Track")
	assert.Contains(t, result, "close")
}

func TestRenderHistory_Empty(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.History = nil

	result := m.RenderHistory()

	assert.Contains(t, result, "No history yet")
}

func TestRenderHistory_Error(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.HistoryErr = assert.AnError

	result := m.RenderHistory()

	assert.Contains(t, result, "failed to load history")
}

func TestView_HistoryFooter(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.Width = 80
	m.Height = 24
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.History = []protocol.HistoryEntry{{Title: "Artist - Track"}}

	result := m.View()

	assert.Contains(t, result, "Artist — Track")
}

// historyEntries returns n history entries for groovesalad, newest first.
func historyEntries(n int) []protocol.HistoryEntry {
	entries := make([]protocol.HistoryEntry, n)
	for i := range entries {
		entries[i] = protocol.HistoryEntry{ChannelID: "groovesalad", Title: fmt.Sprintf("Artist - Track %d", i+1)}
	}
	return entries
}

func TestRenderHistory_ShowsOnlyTheNewestEntriesThatFit(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.History = historyEntries(20)

	result := m.RenderHistory()

	limit := m.historyEntryLimit()
	require.Positive(t, limit)
	require.Less(t, limit, 20, "20 entries do not fit 24 lines")
	assert.Equal(t, historyCardLines+limit, lipgloss.Height(result))
	assert.Contains(t, result, fmt.Sprintf("latest %d of 20", limit), "the cut is announced")
	// Card lines are padded to the full width, hence the trailing space.
	assert.Contains(t, result, "Artist — Track 1 ", "the newest entries are kept")
	assert.Contains(t, result, fmt.Sprintf("Artist — Track %d ", limit))
	assert.NotContains(t, result, fmt.Sprintf("Artist — Track %d ", limit+1))
}

func TestRenderHistory_FlattensLineBreaksInTitles(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.History = []protocol.HistoryEntry{{Title: "Artist -\r\nTwo\nLines"}}

	result := m.RenderHistory()

	assert.Contains(t, result, "Artist — Two Lines")
	assert.Equal(t, historyCardLines+1, lipgloss.Height(result), "one line per entry")
}

func TestMinListHeight_HoldsTheList(t *testing.T) {
	m := newTestModel(t)
	m.List.SetSize(80, minListHeight)

	view := m.List.View()

	assert.LessOrEqual(t, lipgloss.Height(view), minListHeight)
	assert.Contains(t, view, "Groove Salad", "the selected row still shows")
}

// TestView_NeverTallerThanTheWindow checks the view fits the window after
// each message that can grow the chrome around the list. Bubble Tea cuts a
// taller view from the top, header first.
func TestView_NeverTallerThanTheWindow(t *testing.T) {
	long := strings.Repeat("a rather long message ", 8)
	playing := protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
	}
	openHistory := []tea.Msg{
		ServerStateMsg{State: playing},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}},
		HistoryMsg{ChannelID: "groovesalad", Entries: historyEntries(20)},
	}
	withStreamTrouble := playing
	withStreamTrouble.TrackTitle = long
	withStreamTrouble.StreamError = long

	tests := []struct {
		name string
		msgs []tea.Msg
	}{
		{name: "history arrives", msgs: openHistory},
		{name: "history and about", msgs: append(slices.Clone(openHistory), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})},
		{name: "about then history", msgs: append([]tea.Msg{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}}, openHistory...)},
		{name: "history then status bar grows", msgs: append(slices.Clone(openHistory), ServerStateMsg{State: withStreamTrouble})},
		{name: "long track title and stream error", msgs: []tea.Msg{ServerStateMsg{State: withStreamTrouble}}},
		{name: "request error", msgs: []tea.Msg{RequestErrorMsg{Op: "play", Err: errors.New(long)}}},
		{name: "server lost", msgs: []tea.Msg{ServerStateMsg{State: withStreamTrouble}, ServerLostMsg{}}},
		{name: "restart failed", msgs: []tea.Msg{RestartFailedMsg{Err: errors.New(long)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			for _, msg := range tt.msgs {
				m.Update(msg)
			}

			view := m.View()

			assert.LessOrEqual(t, lipgloss.Height(view), 24)
			assert.Contains(t, strings.Split(view, "\n")[1], "SomaFM", "the header stays on screen")
			for _, line := range strings.Split(view, "\n") {
				assert.LessOrEqual(t, lipgloss.Width(line), 80, "no line is wider than the window")
			}
		})
	}
}

func TestRenderAbout_Hidden(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = false

	assert.Empty(t, m.RenderAbout())
}

func TestRenderAbout_ContainsVersionInfo(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true
	m.About = AboutInfo{
		Version: "2.0.0",
		Commit:  "deadbeef",
		Date:    "2024-06-19",
	}

	result := m.RenderAbout()

	assert.Contains(t, result, "2.0.0")
	assert.Contains(t, result, "deadbeef")
	assert.Contains(t, result, "2024-06-19")
	assert.Contains(t, result, "MIT")
	assert.Contains(t, result, "close")
}

func TestRenderNowPlaying_Stopped_InvitesToPlay(t *testing.T) {
	m := newTestModel(t)

	result := m.RenderNowPlaying()

	assert.Contains(t, result, "Nothing playing")
	assert.Contains(t, result, "press enter")
}

func TestRenderNowPlaying_TitleWithoutArtist(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad",
		TrackTitle: "Station ID", Volume: 1,
	})

	assert.Contains(t, m.RenderNowPlaying(), ui.Equalizer(0)+" Station ID")
}

func TestRenderNowPlaying_Muted(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 0})

	assert.Contains(t, m.RenderNowPlaying(), "muted")
}

func TestRenderNowPlaying_FitsTheWidth(t *testing.T) {
	for _, width := range []int{30, 60, 80, 140} {
		m := newTestModel(t)
		m.Width = width
		m.applySnapshot(protocol.PlaybackState{
			Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: strings.Repeat("Groove Salad ", 10),
			TrackTitle: strings.Repeat("Artist ", 10) + "- " + strings.Repeat("Title ", 20), Volume: 0.5,
			StopAt: time.Now().Add(time.Hour).Format(time.RFC3339),
		})

		for _, line := range strings.Split(m.RenderNowPlaying(), "\n") {
			assert.LessOrEqual(t, lipgloss.Width(line), width, "width %d: %q", width, line)
		}
	}
}

func TestView_Loading_ShowsSpinner(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	assert.Contains(t, m.View(), ui.Spinner(0))
}

func TestView_EmptyFavorites(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})
	view := m.View()

	assert.Contains(t, view, "No favorites yet")
	assert.Contains(t, view, "F shows all stations")
	assert.LessOrEqual(t, lipgloss.Height(view), 24)
}

func TestView_SearchWithoutMatches(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("xyzzy")})
	view := m.View()

	assert.Contains(t, view, "No stations match “xyzzy”")
	assert.Contains(t, view, "esc clears the search")
}

func TestRenderHeader_CountsTheView(t *testing.T) {
	m := newTestModel(t)
	m.Favorites = []string{"dronezone", "gone-from-the-catalog"}

	assert.Contains(t, m.RenderHeader(), "Stations · 3")
	m.FavoritesOnly = true
	assert.Contains(t, m.RenderHeader(), "Favorites · 1", "favorites no longer in the catalog do not count")
}

func TestRenderHelp(t *testing.T) {
	m := newTestModel(t)

	short := m.RenderHelp()
	assert.Equal(t, 1, lipgloss.Height(short))
	assert.LessOrEqual(t, lipgloss.Width(short), 80)
	assert.Contains(t, short, "enter play")

	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	full := m.RenderHelp()
	assert.Greater(t, lipgloss.Height(full), 1, "? expands the help")
	assert.Contains(t, full, "favorites-only view")
	for _, line := range strings.Split(full, "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 80)
	}
}

func TestView_TuningShowsInTheList(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Contains(t, m.View(), "Groove Salad  "+ui.Spinner(0)+" tuning in…")
}

// TestView_ShortWindowsKeepTheHeader checks that in windows too short for
// the full chrome, spacing and the short help give way before the header
// is pushed off the top.
func TestView_ShortWindowsKeepTheHeader(t *testing.T) {
	for _, height := range []int{9, 10, 12, 14, 24} {
		m := newTestModel(t)
		m.Update(tea.WindowSizeMsg{Width: 80, Height: height})

		view := m.View()

		assert.LessOrEqual(t, lipgloss.Height(view), height, "height %d", height)
		assert.Contains(t, strings.Join(strings.Split(view, "\n")[:2], "\n"), "SomaFM", "height %d", height)
	}

	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assert.Contains(t, m.View(), "enter play", "a roomy window keeps the help")
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	view := m.View()
	assert.Contains(t, view, "favorites-only view", "the full help fits 24 lines")
	assert.LessOrEqual(t, lipgloss.Height(view), 24)
}

func TestView_HistoryWithNoRoomLeft(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	m.Update(ServerStateMsg{State: protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	m.Update(HistoryMsg{ChannelID: "groovesalad", Entries: historyEntries(20)})

	view := m.View()

	assert.LessOrEqual(t, lipgloss.Height(view), 14)
	assert.Contains(t, strings.Split(view, "\n")[0]+strings.Split(view, "\n")[1], "SomaFM")
}

func TestView_PaginationFitsTheWidth(t *testing.T) {
	m := newTestModel(t)
	many := make([]channels.Channel, 40)
	for i := range many {
		many[i] = channels.Channel{ID: fmt.Sprintf("c%d", i), Title: fmt.Sprintf("Channel %d", i), Listeners: "1"}
	}
	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: many}})

	// 40 pages of dots are 80 cells, plus the indent: too wide for 80.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 14})
	for _, line := range strings.Split(m.View(), "\n") {
		assert.LessOrEqual(t, lipgloss.Width(line), 80)
	}
	assert.Contains(t, m.View(), "page 1 of")

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	assert.NotContains(t, m.View(), "page 1 of", "dots where they fit")
}
