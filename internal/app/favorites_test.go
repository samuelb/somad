package app

import (
	"testing"

	"somatui/internal/channels"
	"somatui/internal/state"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsFavorite_NilState(t *testing.T) {
	m := newTestModel(t)
	m.State = nil

	assert.False(t, m.IsFavorite(0))
}

func TestIsFavorite_OutOfBounds(t *testing.T) {
	m := newTestModel(t)

	assert.False(t, m.IsFavorite(-1))
	assert.False(t, m.IsFavorite(100))
}

func TestIsFavorite_NotFavorite(t *testing.T) {
	m := newTestModel(t)

	assert.False(t, m.IsFavorite(0))
	assert.False(t, m.IsFavorite(1))
}

func TestIsFavorite_IsFavorite(t *testing.T) {
	m := newTestModel(t)
	m.State.FavoriteChannelIDs = []string{"groovesalad"}

	assert.True(t, m.IsFavorite(0))
	assert.False(t, m.IsFavorite(1))
}

func TestToggleFavorite_NilState(t *testing.T) {
	m := newTestModel(t)
	m.State = nil

	// Should return without panicking
	m.ToggleFavorite()
}

func TestToggleFavorite_AddsToFavorites(t *testing.T) {
	m := newTestModel(t)

	// Index 0 = Groove Salad is selected by default
	m.ToggleFavorite()

	require.NotNil(t, m.State)
	assert.True(t, m.State.IsFavorite("groovesalad"))
}

func TestToggleFavorite_RemovesFromFavorites(t *testing.T) {
	m := newTestModel(t)
	m.State.FavoriteChannelIDs = []string{"groovesalad"}

	m.ToggleFavorite()

	assert.False(t, m.State.IsFavorite("groovesalad"))
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

func TestToggleFavorite_SearchMatchesRebuilt(t *testing.T) {
	m := newTestModel(t)
	m.SearchQuery = "zone"
	m.UpdateSearchMatches()
	initialMatch := m.SearchMatches[0]

	// Favorite Drone Zone (index 1 initially)
	m.List.Select(1)
	m.ToggleFavorite()

	// After Drone Zone moves to top (index 0), search matches must reflect new index
	require.NotEmpty(t, m.SearchMatches)
	assert.NotEqual(t, initialMatch, m.SearchMatches[0], "indices should have been rebuilt")
}

func TestSortItemsWithFavorites_NilState(t *testing.T) {
	m := newTestModel(t)
	m.State = nil

	items := m.List.Items()
	result := m.sortItemsWithFavorites(items)

	// Should return items unchanged
	assert.Equal(t, items, result)
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
	m.State.FavoriteChannelIDs = []string{"secretagent"}

	items := m.List.Items()
	result := m.sortItemsWithFavorites(items)

	first, ok := result[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "secretagent", first.Channel.ID)
}

func TestSortItemsWithFavorites_PreservesRelativeOrder(t *testing.T) {
	m := newTestModel(t)
	m.State.FavoriteChannelIDs = []string{"groovesalad", "secretagent"}

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

func TestToggleFavorite_PersistsState(t *testing.T) {
	m := newTestModel(t)

	m.ToggleFavorite()

	// Load state from disk and verify it was saved
	loaded, err := state.LoadState()
	require.NoError(t, err)
	assert.True(t, loaded.IsFavorite("groovesalad"))
}
