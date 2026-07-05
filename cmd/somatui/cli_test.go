package main

import (
	"strings"
	"testing"

	"somatui/internal/channels"
	"somatui/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatChannelList_MarksFavoritesAndKeepsOrder(t *testing.T) {
	payload := protocol.ChannelsPayload{
		Channels: []channels.Channel{
			{ID: "dronezone", Title: "Drone Zone", Genre: "ambient"},
			{ID: "groovesalad", Title: "Groove Salad", Genre: "ambient|electronica"},
			{ID: "secretagent", Title: "Secret Agent", Genre: "lounge"},
		},
		Favorites: []string{"dronezone"},
	}

	out := formatChannelList(payload)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 3)

	// Catalog order is preserved (the server already sorts favorites first).
	assert.Contains(t, lines[0], "dronezone")
	assert.Contains(t, lines[1], "groovesalad")
	assert.Contains(t, lines[2], "secretagent")

	// Favorites are starred, everything else is not.
	assert.True(t, strings.HasPrefix(lines[0], "* "), "favorite must be marked: %q", lines[0])
	assert.True(t, strings.HasPrefix(lines[1], "  "), "non-favorite must not be marked: %q", lines[1])

	// Title and genre columns are present.
	assert.Contains(t, lines[0], "Drone Zone")
	assert.Contains(t, lines[0], "ambient")
	assert.Contains(t, lines[2], "Secret Agent")
	assert.Contains(t, lines[2], "lounge")
}

func TestFormatChannelList_ScriptFriendlyFields(t *testing.T) {
	payload := protocol.ChannelsPayload{
		Channels: []channels.Channel{
			{ID: "groovesalad", Title: "Groove Salad", Genre: "ambient"},
		},
	}

	// Scripts consume the list with awk-style field splitting, so the ID
	// must lead the line, preceded only by the star on favorites.
	fields := strings.Fields(formatChannelList(payload))
	require.NotEmpty(t, fields)
	assert.Equal(t, "groovesalad", fields[0], "unstarred lines lead with the ID")

	payload.Favorites = []string{"groovesalad"}
	fields = strings.Fields(formatChannelList(payload))
	require.GreaterOrEqual(t, len(fields), 2)
	assert.Equal(t, "*", fields[0])
	assert.Equal(t, "groovesalad", fields[1])
}

func TestFormatChannelList_EmptyCatalog(t *testing.T) {
	assert.Empty(t, formatChannelList(protocol.ChannelsPayload{}))
}

func TestParseVolumeArg(t *testing.T) {
	tests := []struct {
		arg      string
		pct      int
		relative bool
		wantErr  bool
	}{
		{arg: "0", pct: 0},
		{arg: "100", pct: 100},
		{arg: "42", pct: 42},
		{arg: "+5", pct: 5, relative: true},
		{arg: "-10", pct: -10, relative: true},
		// Relative adjustments may exceed 100; the server clamps the result.
		{arg: "+200", pct: 200, relative: true},
		{arg: "101", wantErr: true},
		{arg: "-1", pct: -1, relative: true}, // explicit sign means relative
		{arg: "150", wantErr: true},
		{arg: "loud", wantErr: true},
		{arg: "", wantErr: true},
	}
	for _, tt := range tests {
		pct, relative, err := parseVolumeArg(tt.arg)
		if tt.wantErr {
			assert.Error(t, err, "arg %q", tt.arg)
			continue
		}
		require.NoError(t, err, "arg %q", tt.arg)
		assert.Equal(t, tt.pct, pct, "arg %q", tt.arg)
		assert.Equal(t, tt.relative, relative, "arg %q", tt.arg)
	}
}

func TestExtractJSONFlag(t *testing.T) {
	rest, jsonOut := extractJSONFlag([]string{"--json"})
	assert.Empty(t, rest)
	assert.True(t, jsonOut)

	rest, jsonOut = extractJSONFlag([]string{"groovesalad"})
	assert.Equal(t, []string{"groovesalad"}, rest)
	assert.False(t, jsonOut)

	rest, jsonOut = extractJSONFlag([]string{"--json", "groovesalad"})
	assert.Equal(t, []string{"groovesalad"}, rest)
	assert.True(t, jsonOut)
}

func TestChannelListEntries_MarksFavorites(t *testing.T) {
	payload := protocol.ChannelsPayload{
		Channels: []channels.Channel{
			{ID: "dronezone", Title: "Drone Zone", Genre: "ambient"},
			{ID: "groovesalad", Title: "Groove Salad", Genre: "ambient|electronica"},
		},
		Favorites: []string{"dronezone"},
	}

	entries := channelListEntries(payload)

	require.Len(t, entries, 2)
	assert.Equal(t, channelListEntry{ID: "dronezone", Title: "Drone Zone", Genre: "ambient", Favorite: true}, entries[0])
	assert.Equal(t, channelListEntry{ID: "groovesalad", Title: "Groove Salad", Genre: "ambient|electronica", Favorite: false}, entries[1])
}

func TestFavoriteMessage_ReportsToggleDirection(t *testing.T) {
	ch := channels.Channel{ID: "dronezone", Title: "Drone Zone"}

	// The server returns the favorites list after the toggle: the channel
	// being in it means it was just added.
	assert.Equal(t, "Favorited: Drone Zone", favoriteMessage([]string{"groovesalad", "dronezone"}, ch))
	assert.Equal(t, "Unfavorited: Drone Zone", favoriteMessage([]string{"groovesalad"}, ch))
	assert.Equal(t, "Unfavorited: Drone Zone", favoriteMessage(nil, ch))
}
