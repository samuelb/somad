package ui

import (
	"bytes"
	"testing"

	"somad/internal/channels"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
)

func newTestList(channelItems []channels.Channel, playingID *string, matchChecker func(int) bool) (list.Model, StyledDelegate) {
	return newTestListWithFavorites(channelItems, playingID, matchChecker, func(int) bool { return false })
}

func newTestListWithFavorites(channelItems []channels.Channel, playingID *string, matchChecker func(int) bool, favoriteChecker func(int) bool) (list.Model, StyledDelegate) {
	items := make([]list.Item, len(channelItems))
	for i, ch := range channelItems {
		items[i] = Item{Channel: ch}
	}
	delegate := NewStyledDelegate(playingID, matchChecker, favoriteChecker)
	l := list.New(items, delegate, 80, 24)
	l.SetShowTitle(false)
	l.SetFilteringEnabled(false)
	return l, delegate
}

func testChannels() []channels.Channel {
	return []channels.Channel{
		{
			ID:          "groovesalad",
			Title:       "Groove Salad",
			Description: "A nicely chilled plate of ambient beats",
			Genre:       "ambient",
			Listeners:   "1000",
			Playlists: []channels.Playlist{
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
			Playlists: []channels.Playlist{
				{URL: "http://somafm.com/dronezone.pls", Format: "mp3", Quality: "high"},
			},
		},
		{
			ID:          "secretagent",
			Title:       "Secret Agent",
			Description: "The soundtrack for your spy movie marathon",
			Genre:       "lounge|spy",
			Listeners:   "750",
			Playlists: []channels.Playlist{
				{URL: "http://somafm.com/secretagent.pls", Format: "mp3", Quality: "high"},
			},
		},
	}
}

func TestDelegateRender_Normal(t *testing.T) {
	playingID := ""
	l, delegate := newTestList(testChannels(), &playingID, func(int) bool { return false })

	var buf bytes.Buffer
	delegate.Render(&buf, l, 1, l.Items()[1]) // index 1 = Drone Zone, not selected (index 0 is selected by default)

	output := buf.String()
	assert.Contains(t, output, "Drone Zone")
}

func TestDelegateRender_Selected(t *testing.T) {
	playingID := ""
	l, delegate := newTestList(testChannels(), &playingID, func(int) bool { return false })

	// Index 0 is selected by default
	var buf bytes.Buffer
	delegate.Render(&buf, l, 0, l.Items()[0])

	output := buf.String()
	assert.Contains(t, output, "Groove Salad")
}

func TestDelegateRender_Playing(t *testing.T) {
	playingID := "dronezone"
	l, delegate := newTestList(testChannels(), &playingID, func(int) bool { return false })

	var buf bytes.Buffer
	delegate.Render(&buf, l, 1, l.Items()[1]) // Drone Zone is playing but not selected

	output := buf.String()
	assert.Contains(t, output, "Drone Zone")
	assert.Contains(t, output, "▶") // playing indicator
}

func TestDelegateRender_SearchMatch(t *testing.T) {
	playingID := ""
	matchChecker := func(idx int) bool { return idx == 2 }
	l, delegate := newTestList(testChannels(), &playingID, matchChecker)

	var buf bytes.Buffer
	delegate.Render(&buf, l, 2, l.Items()[2]) // Secret Agent is a match but not selected

	output := buf.String()
	assert.Contains(t, output, "Secret Agent")
}

func TestDelegateRender_Favorite(t *testing.T) {
	playingID := ""
	favoriteChecker := func(idx int) bool { return idx == 1 }
	l, delegate := newTestListWithFavorites(testChannels(), &playingID, func(int) bool { return false }, favoriteChecker)

	var buf bytes.Buffer
	delegate.Render(&buf, l, 1, l.Items()[1]) // Drone Zone is favorite but not selected

	output := buf.String()
	assert.Contains(t, output, "Drone Zone")
	assert.Contains(t, output, "♥") // favorite indicator
}

func TestDelegateRender_InvalidItem(t *testing.T) {
	playingID := ""
	l, delegate := newTestList(testChannels(), &playingID, func(int) bool { return false })

	// Pass an item that isn't our `Item` type — should return without writing
	var buf bytes.Buffer
	delegate.Render(&buf, l, 0, mockListItem{})

	assert.Empty(t, buf.String())
}

// mockListItem is a list.Item that is not our `Item` type.
type mockListItem struct{}

func (m mockListItem) FilterValue() string { return "" }

func TestItemMethods(t *testing.T) {
	ch := channels.Channel{
		Title:       "Groove Salad",
		Description: "Ambient beats",
		Listeners:   "1234",
	}
	i := Item{Channel: ch}

	assert.Equal(t, "Groove Salad", i.Title())
	assert.Equal(t, "Ambient beats", i.Description())
	assert.Equal(t, "Groove Salad", i.FilterValue())
	assert.Equal(t, "1234", i.Listeners())
}
