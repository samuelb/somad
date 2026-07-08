package main

import (
	"strings"
	"testing"

	"somad/internal/channels"
	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testCatalog = []channels.Channel{
	{ID: "dronezone", Title: "Drone Zone", Genre: "ambient"},
	// "dronezone" is also a substring of this ID, so an exact-ID query must
	// resolve to the channel above rather than report an ambiguous match.
	{ID: "dronezonedeep", Title: "Drone Zone Deep", Genre: "ambient"},
	{ID: "groovesalad", Title: "Groove Salad", Genre: "ambient|electronica"},
	{ID: "secretagent", Title: "Secret Agent", Genre: "lounge"},
	{ID: "deepspaceone", Title: "Deep Space One", Genre: "ambient|space"},
}

func TestResolveChannel(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		wantID  string
		wantErr string // substring the error must contain; empty means success
	}{
		{name: "exact id", query: "groovesalad", wantID: "groovesalad"},
		// "dronezone" is an exact ID and a substring of "dronezonedeep";
		// the exact match must win instead of erroring as ambiguous.
		{name: "exact id beats substring", query: "dronezone", wantID: "dronezone"},
		{name: "unique id substring", query: "secret", wantID: "secretagent"},
		{name: "unique title substring", query: "Groove", wantID: "groovesalad"},
		{name: "case-insensitive title", query: "deep space", wantID: "deepspaceone"},
		{name: "no match", query: "jazz", wantErr: "no channel matches"},
		// "one" is a substring of both "dronezone" and "deepspaceone".
		{name: "ambiguous substring", query: "one", wantErr: "ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch, err := resolveChannel(testCatalog, tt.query)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, ch.ID)
		})
	}
}

func TestResolveChannel_AmbiguousListsMatches(t *testing.T) {
	// An ambiguous query must name each candidate (as "id (Title)") so the
	// user can disambiguate.
	catalog := []channels.Channel{
		{ID: "spacestation", Title: "Space Station"},
		{ID: "deepspace", Title: "Deep Space"},
	}
	_, err := resolveChannel(catalog, "space")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spacestation (Space Station)")
	assert.Contains(t, err.Error(), "deepspace (Deep Space)")
}

func TestFindChannelByID(t *testing.T) {
	ch, ok := findChannelByID(testCatalog, "secretagent")
	require.True(t, ok)
	assert.Equal(t, "Secret Agent", ch.Title)

	_, ok = findChannelByID(testCatalog, "SecretAgent") // IDs are exact, case-sensitive
	assert.False(t, ok)

	_, ok = findChannelByID(testCatalog, "missing")
	assert.False(t, ok)

	_, ok = findChannelByID(nil, "anything")
	assert.False(t, ok)
}

func TestVolumePercent(t *testing.T) {
	tests := []struct {
		v    float64
		want int
	}{
		{v: 0, want: 0},
		{v: 1, want: 100},
		{v: 0.5, want: 50},
		{v: 0.004, want: 0},   // rounds down
		{v: 0.005, want: 1},   // rounds up at the half
		{v: 0.666, want: 67},  // standard rounding
		{v: 0.995, want: 100}, // rounds up to full
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, volumePercent(tt.v), "volumePercent(%v)", tt.v)
	}
}

func TestPrintUsage(t *testing.T) {
	var b strings.Builder
	printUsage(&b)
	out := b.String()
	// Every user-facing subcommand should be documented in the usage text.
	for _, cmd := range []string{"play", "list", "favorite", "next", "prev", "pause", "stop", "status", "volume", "daemon"} {
		assert.Containsf(t, out, "soma "+cmd, "usage missing %q", cmd)
	}
}

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
