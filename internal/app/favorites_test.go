package app

import (
	"fmt"
	"slices"
	"testing"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToggleFavorite_AddsToFavorites(t *testing.T) {
	m := newTestModel(t)

	// Index 0 = Groove Salad is selected by default
	cmd := m.ToggleFavorite()

	assert.Contains(t, m.Favorites, "groovesalad")
	// The command persists the toggle on the server.
	runCmd(cmd)
	assert.Equal(t, []string{"groovesalad"}, backend(m).favorites)
}

func TestToggleFavorite_RemovesFromFavorites(t *testing.T) {
	m := newTestModel(t)
	m.Favorites = []string{"groovesalad"}

	m.ToggleFavorite()

	assert.NotContains(t, m.Favorites, "groovesalad")
}

func TestToggleFavorite_NoBackend_DoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.Backend = nil

	cmd := m.ToggleFavorite()

	assert.Contains(t, m.Favorites, "groovesalad")
	runCmd(cmd) // must not panic
}

func TestToggleFavorite_FavoritesMovedToTop(t *testing.T) {
	m := newTestModel(t)
	// Select index 1 (Drone Zone)
	m.List.Select(1)

	m.ToggleFavorite()

	// After toggling, Drone Zone should be at the top
	first, ok := m.List.Items()[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", first.Channel.ID)
}

func TestToggleFavorite_CursorFollowsChannel(t *testing.T) {
	m := newTestModel(t)
	// Select index 1 (Drone Zone)
	m.List.Select(1)

	m.ToggleFavorite()

	// Cursor should now be on Drone Zone (which moved to top)
	selected, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", selected.Channel.ID)
}

func TestToggleFavorite_WhileSearching_KeepsMatchesOnlyView(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "ambient"
	m.UpdateSearchMatches()
	// Both Groove Salad's and Drone Zone's descriptions mention "ambient".
	require.Equal(t, 2, m.matchCount())
	require.Len(t, m.List.Items(), 2)

	sel, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	selectedID := sel.Channel.ID

	m.ToggleFavorite()

	assert.Contains(t, m.Favorites, selectedID)
	// Favoriting a channel does not remove it from an active search filter;
	// the matches-only view still shows exactly the matches.
	assert.Len(t, m.List.Items(), 2)
	assert.Equal(t, 2, m.matchCount())
}

// favoritesView returns a model showing only its favorites, all n of its
// channels, c0 to c<n-1>, with the cursor on index at.
func favoritesView(t *testing.T, n, at int) *Model {
	t.Helper()
	m := newTestModel(t)
	chans := make([]channels.Channel, n)
	favs := make([]string, n)
	for i := range chans {
		chans[i] = channels.Channel{ID: fmt.Sprintf("c%d", i), Title: fmt.Sprintf("Channel %d", i), Listeners: "1"}
		favs[i] = chans[i].ID
	}
	backend(m).favorites = slices.Clone(favs)
	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: chans, Favorites: favs}})
	sendKey(m, 'F')
	require.Len(t, m.List.Items(), n)
	m.List.Select(at)
	return m
}

func TestToggleFavorite_InFavoritesViewKeepsTheCursorInPlace(t *testing.T) {
	m := favoritesView(t, 12, 8)
	require.Equal(t, "c8", m.selectedChannelID())

	_, cmd := sendKey(m, 'f')

	require.Len(t, m.List.Items(), 11, "the channel left the view")
	assert.Equal(t, 8, m.List.Index(), "the cursor stays where it was")
	assert.Equal(t, "c9", m.selectedChannelID(), "on the channel that moved up")

	// The server's answer to the toggle leaves it there.
	m.Update(runCmd(cmd))
	assert.Equal(t, 8, m.List.Index())
	assert.Equal(t, "c9", m.selectedChannelID())
}

func TestToggleFavorite_InFavoritesViewOnTheLastItemMovesToTheNewLast(t *testing.T) {
	m := favoritesView(t, 12, 11)

	sendKey(m, 'f')

	assert.Equal(t, 10, m.List.Index())
	assert.Equal(t, "c10", m.selectedChannelID())
}

func TestToggleFavorite_InFavoritesViewOnTheOnlyItemLeavesItEmpty(t *testing.T) {
	m := favoritesView(t, 1, 0)

	sendKey(m, 'f')

	assert.Empty(t, m.List.Items())
	assert.Equal(t, 0, m.List.Index())
}

// Opening the favorites view from a channel that is not in it is a change
// of view, not a channel leaving the one on screen: it starts at the top.
func TestFavoritesOnlyKey_FromANonFavoriteStartsAtTheTop(t *testing.T) {
	m := favoritesView(t, 12, 0)
	sendKey(m, 'F') // back to all channels
	m.Update(FavoritesMsg{Favorites: []string{"c0", "c1", "c2", "c3", "c4"}})
	m.List.Select(10)
	require.Equal(t, "c10", m.selectedChannelID())

	sendKey(m, 'F')

	require.Len(t, m.List.Items(), 5)
	assert.Equal(t, 0, m.List.Index())

	// From a favorite, the cursor stays on it.
	sendKey(m, 'F')
	m.List.Select(3)
	sendKey(m, 'F')
	assert.Equal(t, "c3", m.selectedChannelID())
}

func TestSortItemsWithFavorites_NoFavorites(t *testing.T) {
	m := newTestModel(t)

	items := m.List.Items()
	result := m.sortItemsWithFavorites(items)

	// Order preserved when no favorites
	require.Len(t, result, len(items))
	for i, item := range result {
		ri, _ := item.(ui.Item)
		oi, _ := items[i].(ui.Item)
		assert.Equal(t, oi.Channel.ID, ri.Channel.ID)
	}
}

func TestSortItemsWithFavorites_FavoritesFirst(t *testing.T) {
	m := newTestModel(t)
	m.Favorites = []string{"secretagent"}

	items := m.List.Items()
	result := m.sortItemsWithFavorites(items)

	first, ok := result[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "secretagent", first.Channel.ID)
}

func TestSortItemsWithFavorites_PreservesRelativeOrder(t *testing.T) {
	m := newTestModel(t)
	m.Favorites = []string{"groovesalad", "secretagent"}

	chans := []channels.Channel{
		{ID: "groovesalad"}, {ID: "dronezone"}, {ID: "secretagent"},
	}
	items := make([]list.Item, len(chans))
	for i, ch := range chans {
		items[i] = ui.Item{Channel: ch}
	}

	result := m.sortItemsWithFavorites(items)

	ids := make([]string, len(result))
	for i, r := range result {
		ri, _ := r.(ui.Item)
		ids[i] = ri.Channel.ID
	}
	// Both favorites first (in their original relative order), then non-favorite
	assert.Equal(t, []string{"groovesalad", "secretagent", "dronezone"}, ids)
}
