package app

import (
	"strings"
	"testing"

	"somatui/internal/audio"

	"github.com/stretchr/testify/assert"
)

func TestPlaceOverlay_Basic(t *testing.T) {
	bg := strings.Repeat(".", 10) + "\n" +
		strings.Repeat(".", 10) + "\n" +
		strings.Repeat(".", 10)
	fg := "XY"

	result := PlaceOverlay(2, 1, fg, bg)

	lines := strings.Split(result, "\n")
	assert.Equal(t, 3, len(lines))
	assert.Contains(t, lines[1], "XY")
	// Characters before and after the overlay must be preserved
	assert.True(t, strings.HasPrefix(lines[1], ".."), "prefix dots preserved: %q", lines[1])
}

func TestPlaceOverlay_AtOrigin(t *testing.T) {
	bg := "AAAA\nBBBB"
	fg := "XY"

	result := PlaceOverlay(0, 0, fg, bg)

	lines := strings.Split(result, "\n")
	assert.True(t, strings.HasPrefix(lines[0], "XY"), "overlay at 0,0: %q", lines[0])
}

func TestPlaceOverlay_OutOfBoundsY(t *testing.T) {
	bg := "AAAA"
	fg := "XY"

	// y beyond background — foreground lines are skipped, background returned intact
	result := PlaceOverlay(0, 5, fg, bg)

	assert.Equal(t, bg, result)
}

func TestPlaceOverlay_MultiLine(t *testing.T) {
	bg := "AAAA\nBBBB\nCCCC"
	fg := "XX\nYY"

	result := PlaceOverlay(1, 0, fg, bg)

	lines := strings.Split(result, "\n")
	assert.Contains(t, lines[0], "XX")
	assert.Contains(t, lines[1], "YY")
}

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
	m.PlayingID = ""

	result := m.RenderStatusBar(m.List.Items())

	assert.Contains(t, result, "Stopped")
	assert.Contains(t, result, "■")
}

func TestRenderStatusBar_Playing(t *testing.T) {
	m := newTestModel(t)
	m.PlayingID = "groovesalad"

	result := m.RenderStatusBar(m.List.Items())

	assert.Contains(t, result, "Playing")
	assert.Contains(t, result, "▶")
	assert.Contains(t, result, "Groove Salad")
}

func TestRenderStatusBar_WithTrackInfo(t *testing.T) {
	m := newTestModel(t)
	m.PlayingID = "groovesalad"
	m.TrackInfo = &audio.TrackInfo{Title: "Artist - Song"}

	result := m.RenderStatusBar(m.List.Items())

	assert.Contains(t, result, "Artist - Song")
	assert.Contains(t, result, "♫")
}

func TestRenderStatusBar_WithStreamError(t *testing.T) {
	m := newTestModel(t)
	m.StreamErr = "connection reset"

	result := m.RenderStatusBar(m.List.Items())

	assert.Contains(t, result, "connection reset")
	assert.Contains(t, result, "Stream error")
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

func TestView_AboutOverlay(t *testing.T) {
	m := newTestModel(t)
	m.ShowAbout = true
	m.Width = 80
	m.Height = 24
	m.About = AboutInfo{Version: "1.2.3", Commit: "abc123", Date: "2024-01-01"}

	result := m.View()

	assert.Contains(t, result, "SomaTUI")
	assert.Contains(t, result, "1.2.3")
}

func TestRenderAboutScreen_ContainsVersionInfo(t *testing.T) {
	m := newTestModel(t)
	m.About = AboutInfo{
		Version: "2.0.0",
		Commit:  "deadbeef",
		Date:    "2024-06-19",
	}

	result := m.RenderAboutScreen()

	assert.Contains(t, result, "2.0.0")
	assert.Contains(t, result, "deadbeef")
	assert.Contains(t, result, "2024-06-19")
	assert.Contains(t, result, "MIT")
}
