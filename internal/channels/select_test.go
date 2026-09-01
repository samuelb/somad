package channels

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func urls(playlists []Playlist) []string {
	out := make([]string, len(playlists))
	for i, pl := range playlists {
		out[i] = pl.URL
	}
	return out
}

func TestSelectPlaylists_FindsMP3(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"})

	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls"}, urls(got))
}

func TestSelectPlaylists_PrefersHighestQuality(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad64.pls", Format: "mp3", Quality: "low"},
		{URL: "http://somafm.com/groovesalad130.pls", Format: "mp3", Quality: "highest"},
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "high"},
		{URL: "http://somafm.com/groovesalad256.pls", Format: "aac", Quality: "highest"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"})

	assert.Equal(t, []string{"http://somafm.com/groovesalad130.pls"}, urls(got))
}

func TestSelectPlaylists_UnknownQualityStillSelected(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "experimental"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"})

	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls"}, urls(got))
}

func TestSelectPlaylists_KnownQualityBeatsUnknown(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/unknown.pls", Format: "mp3", Quality: ""},
		{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"})

	assert.Equal(t, []string{"http://somafm.com/low.pls"}, urls(got))
}

func TestSelectPlaylists_FormatOrderIsPreferenceOrder(t *testing.T) {
	// Mirrors a real SomaFM channel: MP3 + AAC-LC + two HE-AAC tiers.
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "highest"},
		{URL: "http://somafm.com/groovesalad130.pls", Format: "aac", Quality: "highest"},
		{URL: "http://somafm.com/groovesalad64.pls", Format: "aacp", Quality: "high"},
		{URL: "http://somafm.com/groovesalad32.pls", Format: "aacp", Quality: "low"},
	}

	got := SelectPlaylists(playlists, []string{"aac", "mp3"})

	// AAC leads, MP3 is the fallback, aacp is never selected.
	assert.Equal(t, []string{
		"http://somafm.com/groovesalad130.pls",
		"http://somafm.com/groovesalad.pls",
	}, urls(got))
}

func TestSelectPlaylists_MissingFormatSkipped(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "highest"},
	}

	got := SelectPlaylists(playlists, []string{"aac", "mp3"})

	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls"}, urls(got))
}

func TestSelectPlaylists_NoMatchingFormat(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
	}

	assert.Empty(t, SelectPlaylists(playlists, []string{"mp3"}))
}

func TestSelectPlaylists_Empty(t *testing.T) {
	assert.Empty(t, SelectPlaylists(nil, []string{"mp3"}))
	assert.Empty(t, SelectPlaylists([]Playlist{}, []string{"aac", "mp3"}))
}
