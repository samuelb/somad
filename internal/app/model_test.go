package app

import (
	"testing"

	"somatui/internal/audio"
	"somatui/internal/channels"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopMetadataReader_Nil(t *testing.T) {
	m := newTestModel(t)
	m.MetadataReader = nil

	// Should not panic
	m.StopMetadataReader()
	assert.Nil(t, m.MetadataReader)
}

func TestStopMetadataReader_Active(t *testing.T) {
	m := newTestModel(t)
	m.MetadataReader = audio.NewMetadataReader("http://somafm.com/stream.mp3")

	m.StopMetadataReader()

	assert.Nil(t, m.MetadataReader)
}

func TestStopMetadataReader_SafeToCallTwice(t *testing.T) {
	m := newTestModel(t)
	m.MetadataReader = audio.NewMetadataReader("http://somafm.com/stream.mp3")

	// Both calls should succeed without panic
	m.StopMetadataReader()
	m.StopMetadataReader()
}

func TestSelectMP3PlaylistURL_FindsMP3(t *testing.T) {
	playlists := []channels.Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Equal(t, "http://somafm.com/groovesalad.pls", got)
}

func TestSelectMP3PlaylistURL_NoMP3(t *testing.T) {
	playlists := []channels.Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Empty(t, got)
}

func TestSelectMP3PlaylistURL_Empty(t *testing.T) {
	assert.Empty(t, SelectMP3PlaylistURL(nil))
	assert.Empty(t, SelectMP3PlaylistURL([]channels.Playlist{}))
}

func TestGetPlayingChannel_NotPlaying(t *testing.T) {
	m := newTestModel(t)

	got := m.GetPlayingChannel(m.List.Items())

	assert.Nil(t, got)
}

func TestGetPlayingChannel_Playing(t *testing.T) {
	m := newTestModel(t)
	m.PlayingID = "dronezone"

	got := m.GetPlayingChannel(m.List.Items())

	require.NotNil(t, got)
	assert.Equal(t, "dronezone", got.ID)
}

func TestGetPlayingChannel_IDNotInList(t *testing.T) {
	m := newTestModel(t)
	m.PlayingID = "nonexistent"

	got := m.GetPlayingChannel(m.List.Items())

	assert.Nil(t, got)
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
	full, short := NewHelpKeys()

	assert.NotEmpty(t, full)
	assert.NotEmpty(t, short)

	// Short help should be a subset of or equal in length to full help
	assert.LessOrEqual(t, len(short), len(full))
}

// Verify that list.Item interface is satisfied — compile-time check.
var _ list.Item = ui.Item{}
