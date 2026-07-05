package channels

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSelectMP3PlaylistURL_FindsMP3(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Equal(t, "http://somafm.com/groovesalad.pls", got)
}

func TestSelectMP3PlaylistURL_PrefersHighestQuality(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad64.pls", Format: "mp3", Quality: "low"},
		{URL: "http://somafm.com/groovesalad130.pls", Format: "mp3", Quality: "highest"},
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "high"},
		{URL: "http://somafm.com/groovesalad256.pls", Format: "aac", Quality: "highest"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Equal(t, "http://somafm.com/groovesalad130.pls", got)
}

func TestSelectMP3PlaylistURL_UnknownQualityStillSelected(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "experimental"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Equal(t, "http://somafm.com/groovesalad.pls", got)
}

func TestSelectMP3PlaylistURL_KnownQualityBeatsUnknown(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/unknown.pls", Format: "mp3", Quality: ""},
		{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Equal(t, "http://somafm.com/low.pls", got)
}

func TestSelectMP3PlaylistURL_NoMP3(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
	}

	got := SelectMP3PlaylistURL(playlists)

	assert.Empty(t, got)
}

func TestSelectMP3PlaylistURL_Empty(t *testing.T) {
	assert.Empty(t, SelectMP3PlaylistURL(nil))
	assert.Empty(t, SelectMP3PlaylistURL([]Playlist{}))
}
