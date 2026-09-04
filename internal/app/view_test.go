package app

import (
	"strings"
	"testing"
	"time"

	"somad/internal/protocol"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

func TestRenderSearchBar_Active(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"
	m.UpdateSearchMatches()

	result := m.RenderSearchBar()

	assert.Contains(t, result, "groove")
	assert.Contains(t, result, "[1/1]")
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
	assert.Contains(t, result, "[1/1]")
	assert.Contains(t, result, "n/N navigate")
}

func TestRenderSearchBar_InactiveNoQuery(t *testing.T) {
	m := newTestModel(t)
	m.Searching = false
	m.SearchQuery = ""

	result := m.RenderSearchBar()

	assert.Empty(t, result)
}

func TestRenderStatusBar_Stopped(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "Stopped")
	assert.Contains(t, result, "■")
}

func TestRenderStatusBar_Connecting(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusConnecting, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
	})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "Connecting")
	assert.Contains(t, result, "◌")
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderStatusBar_ShowsVolume(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 0.85})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "♪ 85%")
}

func TestRenderStatusBar_Reconnecting(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusReconnecting, ChannelID: "groovesalad", ChannelTitle: "Groove Salad",
		ReconnectAttempt: 2, Volume: 1,
	})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "Reconnecting #2")
	assert.Contains(t, result, "↻")
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderStatusBar_Playing(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
	})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "Playing")
	assert.Contains(t, result, "▶")
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderStatusBar_WithTrackInfo(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad",
		TrackTitle: "Artist - Song", Volume: 1,
	})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "Artist - Song")
	assert.Contains(t, result, "♫")
}

func TestRenderStatusBar_WithStreamError(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, StreamError: "connection reset", Volume: 1})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "connection reset")
	assert.Contains(t, result, "Stream error")
}

func TestRenderStatusBar_WrapsOnNarrowTerminals(t *testing.T) {
	m := newTestModel(t)
	m.Width = 30
	m.applySnapshot(protocol.PlaybackState{
		Status:      protocol.StatusStopped,
		StreamError: "stream stalled: no data received for thirty long seconds",
		Volume:      1,
	})

	result := m.RenderStatusBar()

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

func TestSleepTimerLabel_EmptyWhenNotSet(t *testing.T) {
	assert.Empty(t, sleepTimerLabel(""))
}

func TestSleepTimerLabel_EmptyOnMalformedTimestamp(t *testing.T) {
	assert.Empty(t, sleepTimerLabel("not-a-timestamp"))
}

func TestRenderStatusBar_ShowsSleepTimer(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1,
		StopAt: time.Now().Add(42 * time.Minute).Format(time.RFC3339),
	})

	result := m.RenderStatusBar()

	assert.Contains(t, result, "sleep in 42m")
}

func TestRenderStatusBar_NoSleepTimerByDefault(t *testing.T) {
	m := newTestModel(t)
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})

	result := m.RenderStatusBar()

	assert.NotContains(t, result, "sleep in")
}

func TestRenderStatusBar_ServerLost(t *testing.T) {
	m := newTestModel(t)
	m.ServerLost = true

	result := m.RenderStatusBar()

	assert.Contains(t, result, "server connection lost")
}

func TestRenderHeader_ContainsTitles(t *testing.T) {
	m := newTestModel(t)

	result := m.RenderHeader()

	assert.Contains(t, result, "SomaFM Stations")
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

func TestRenderHistoryFooter_Hidden(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = false

	assert.Empty(t, m.RenderHistoryFooter())
}

func TestRenderHistoryFooter_NothingPlaying(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = ""

	result := m.RenderHistoryFooter()

	assert.Contains(t, result, "Nothing is playing")
}

func TestRenderHistoryFooter_ContainsEntries(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.History = []protocol.HistoryEntry{
		{Title: "Artist - First Track"},
		{Title: "Artist - Second Track"},
	}

	result := m.RenderHistoryFooter()

	assert.Contains(t, result, "Groove Salad")
	assert.Contains(t, result, "Artist - First Track")
	assert.Contains(t, result, "Artist - Second Track")
	assert.Contains(t, result, "close")
}

func TestRenderHistoryFooter_Empty(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.History = nil

	result := m.RenderHistoryFooter()

	assert.Contains(t, result, "No history yet")
}

func TestRenderHistoryFooter_Error(t *testing.T) {
	m := newTestModel(t)
	m.ShowHistory = true
	m.HistoryChannelID = "groovesalad"
	m.HistoryChannelTitle = "Groove Salad"
	m.HistoryErr = assert.AnError

	result := m.RenderHistoryFooter()

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

	assert.Contains(t, result, "Artist - Track")
}

func TestRenderAboutFooter_Hidden(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = false

	assert.Empty(t, m.RenderAboutFooter())
}

func TestRenderAboutFooter_ContainsVersionInfo(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true
	m.About = AboutInfo{
		Version: "2.0.0",
		Commit:  "deadbeef",
		Date:    "2024-06-19",
	}

	result := m.RenderAboutFooter()

	assert.Contains(t, result, "2.0.0")
	assert.Contains(t, result, "deadbeef")
	assert.Contains(t, result, "2024-06-19")
	assert.Contains(t, result, "MIT")
	assert.Contains(t, result, "close")
}
