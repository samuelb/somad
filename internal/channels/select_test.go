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

	got := SelectPlaylists(playlists, []string{"mp3"}, "")

	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls"}, urls(got))
}

func TestSelectPlaylists_PrefersHighestQuality(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad64.pls", Format: "mp3", Quality: "low"},
		{URL: "http://somafm.com/groovesalad130.pls", Format: "mp3", Quality: "highest"},
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "high"},
		{URL: "http://somafm.com/groovesalad256.pls", Format: "aac", Quality: "highest"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"}, "")

	assert.Equal(t, []string{"http://somafm.com/groovesalad130.pls"}, urls(got))
}

func TestSelectPlaylists_UnknownQualityStillSelected(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "experimental"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"}, "")

	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls"}, urls(got))
}

func TestSelectPlaylists_KnownQualityBeatsUnknown(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/unknown.pls", Format: "mp3", Quality: ""},
		{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"}, "")

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

	got := SelectPlaylists(playlists, []string{"aac", "mp3"}, "")

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

	got := SelectPlaylists(playlists, []string{"aac", "mp3"}, "")

	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls"}, urls(got))
}

func TestSelectPlaylists_NoMatchingFormat(t *testing.T) {
	playlists := []Playlist{
		{URL: "http://somafm.com/groovesalad.aac", Format: "aac"},
	}

	assert.Empty(t, SelectPlaylists(playlists, []string{"mp3"}, ""))
}

func TestSelectPlaylists_Empty(t *testing.T) {
	assert.Empty(t, SelectPlaylists(nil, []string{"mp3"}, ""))
	assert.Empty(t, SelectPlaylists([]Playlist{}, []string{"aac", "mp3"}, ""))
}

func TestSelectPlaylists_PrefersHTTPS(t *testing.T) {
	tests := []struct {
		name      string
		playlists []Playlist
		want      string
	}{
		{
			name: "https entry beats an earlier equal-quality http entry",
			playlists: []Playlist{
				{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "highest"},
				{URL: "https://somafm.com/groovesalad2.pls", Format: "mp3", Quality: "highest"},
			},
			want: "https://somafm.com/groovesalad2.pls",
		},
		{
			name: "https scheme match is case-insensitive",
			playlists: []Playlist{
				{URL: "http://somafm.com/groovesalad.pls", Format: "mp3", Quality: "highest"},
				{URL: "HTTPS://somafm.com/groovesalad2.pls", Format: "mp3", Quality: "highest"},
			},
			want: "HTTPS://somafm.com/groovesalad2.pls",
		},
		{
			name: "a lower-quality https entry does not beat a better-quality http entry",
			playlists: []Playlist{
				{URL: "https://somafm.com/low.pls", Format: "mp3", Quality: "low"},
				{URL: "http://somafm.com/highest.pls", Format: "mp3", Quality: "highest"},
			},
			want: "http://somafm.com/highest.pls",
		},
		{
			name: "https already first is kept",
			playlists: []Playlist{
				{URL: "https://somafm.com/groovesalad.pls", Format: "mp3", Quality: "highest"},
				{URL: "http://somafm.com/groovesalad2.pls", Format: "mp3", Quality: "highest"},
			},
			want: "https://somafm.com/groovesalad.pls",
		},
		{
			name: "no https available falls back to the best-quality http entry",
			playlists: []Playlist{
				{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
				{URL: "http://somafm.com/highest.pls", Format: "mp3", Quality: "highest"},
			},
			want: "http://somafm.com/highest.pls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectPlaylists(tt.playlists, []string{"mp3"}, "")
			assert.Equal(t, []string{tt.want}, urls(got))
		})
	}
}

func TestSelectPlaylists_QualityPreference(t *testing.T) {
	all := []Playlist{
		{URL: "http://somafm.com/highest.pls", Format: "mp3", Quality: "highest"},
		{URL: "http://somafm.com/high.pls", Format: "mp3", Quality: "high"},
		{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
	}

	tests := []struct {
		name      string
		playlists []Playlist
		quality   string
		want      string
	}{
		{
			name:      "empty quality means no preference: best available",
			playlists: all,
			quality:   "",
			want:      "http://somafm.com/highest.pls",
		},
		{
			name:      "unrecognized quality means no preference: best available",
			playlists: all,
			quality:   "ultra",
			want:      "http://somafm.com/highest.pls",
		},
		{
			name:      "exact match: highest",
			playlists: all,
			quality:   "highest",
			want:      "http://somafm.com/highest.pls",
		},
		{
			name:      "exact match: high",
			playlists: all,
			quality:   "high",
			want:      "http://somafm.com/high.pls",
		},
		{
			name:      "exact match: low",
			playlists: all,
			quality:   "low",
			want:      "http://somafm.com/low.pls",
		},
		{
			name: "preferred quality absent: falls back to the nearest available",
			playlists: []Playlist{
				{URL: "http://somafm.com/highest.pls", Format: "mp3", Quality: "highest"},
				{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
			},
			quality: "high", // equidistant from highest and low
			want:    "http://somafm.com/highest.pls",
		},
		{
			name: "preferred quality absent: falls back to the only one available",
			playlists: []Playlist{
				{URL: "http://somafm.com/low.pls", Format: "mp3", Quality: "low"},
			},
			quality: "highest",
			want:    "http://somafm.com/low.pls",
		},
		{
			name: "low preferred, only highest available",
			playlists: []Playlist{
				{URL: "http://somafm.com/highest.pls", Format: "mp3", Quality: "highest"},
			},
			quality: "low",
			want:    "http://somafm.com/highest.pls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SelectPlaylists(tt.playlists, []string{"mp3"}, tt.quality)
			assert.Equal(t, []string{tt.want}, urls(got))
		})
	}
}

func TestSelectPlaylists_QualityPreferenceThenHTTPS(t *testing.T) {
	// Two candidates equally close to the preferred quality (here, an exact
	// match on both): the https one wins.
	playlists := []Playlist{
		{URL: "http://somafm.com/high-a.pls", Format: "mp3", Quality: "high"},
		{URL: "https://somafm.com/high-b.pls", Format: "mp3", Quality: "high"},
	}

	got := SelectPlaylists(playlists, []string{"mp3"}, "high")

	assert.Equal(t, []string{"https://somafm.com/high-b.pls"}, urls(got))
}
