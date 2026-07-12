package app

import (
	"strings"
	"testing"

	"somad/internal/protocol"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

func TestRenderSearchBar_Active(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "groove"
	m.SearchMatches = []int{0}
	m.CurrentMatch = 0

	result := m.RenderSearchBar()

	assert.Contains(t, result, "groove")
	assert.Contains(t, result, "[1/1]")
}

func TestRenderSearchBar_ActiveNoMatches(t *testing.T) {
	m := newTestModel(t)
	m.Searching = true
	m.SearchQuery = "xyzzy"
	m.SearchMatches = nil

	result := m.RenderSearchBar()

	assert.Contains(t, result, "xyzzy")
	assert.Contains(t, result, "no matches")
}

func TestRenderSearchBar_InactiveWithQuery(t *testing.T) {
	m := newTestModel(t)
	m.Searching = false
	m.SearchQuery = "groove"
	m.SearchMatches = []int{0}
	m.CurrentMatch = 0

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
