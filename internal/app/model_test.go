package app

import (
	"testing"

	"somad/internal/channels"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectChannelByID_MovesCursor(t *testing.T) {
	m := newTestModel(t)

	m.selectChannelByID("dronezone")

	sel, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", sel.Channel.ID)
}

func TestSelectChannelByID_UnknownIDKeepsCursor(t *testing.T) {
	m := newTestModel(t)
	m.List.Select(1)

	m.selectChannelByID("nonexistent")

	sel, ok := m.List.SelectedItem().(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "dronezone", sel.Channel.ID)
}

func TestChannelsToItems_Conversion(t *testing.T) {
	chans := []channels.Channel{
		{ID: "a", Title: "Alpha"},
		{ID: "b", Title: "Beta"},
	}

	items := ChannelsToItems(chans)

	require.Len(t, items, 2)
	a, ok := items[0].(ui.Item)
	require.True(t, ok)
	assert.Equal(t, "a", a.Channel.ID)
	assert.Equal(t, "Alpha", a.Channel.Title)
}

func TestChannelsToItems_Empty(t *testing.T) {
	items := ChannelsToItems(nil)
	assert.Empty(t, items)

	items = ChannelsToItems([]channels.Channel{})
	assert.Empty(t, items)
}

func TestUpdateListSize_DoesNotPanic(t *testing.T) {
	m := newTestModel(t)

	// Should not panic with zero dimensions
	m.Width = 0
	m.Height = 0
	m.UpdateListSize()

	m.Width = 80
	m.Height = 24
	m.UpdateListSize()
}

func TestChannelsToItems_PreservesOrder(t *testing.T) {
	chans := testChannels()
	items := ChannelsToItems(chans)

	require.Len(t, items, len(chans))
	for i, ch := range chans {
		item, ok := items[i].(ui.Item)
		require.True(t, ok)
		assert.Equal(t, ch.ID, item.Channel.ID)
	}
}

func TestNewHelpKeys_ReturnsBindings(t *testing.T) {
	full, short := NewHelpKeys(false)

	assert.NotEmpty(t, full)
	assert.NotEmpty(t, short)

	// Short help should be a subset of or equal in length to full help
	assert.LessOrEqual(t, len(short), len(full))
}

// Verify that list.Item interface is satisfied — compile-time check.
var _ list.Item = ui.Item{}
