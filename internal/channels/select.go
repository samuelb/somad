package channels

// qualityRank orders SomaFM playlist quality levels, best first.
var qualityRank = map[string]int{"highest": 0, "high": 1, "low": 2}

// SelectPlaylists returns the playlists to try, in playback-preference
// order: for each format in formats (most preferred first, e.g. AAC before
// MP3 where the platform decodes it) the best-quality playlist of that
// format. The caller works through the result until one connects, so a
// format whose stream fails still falls back to the next.
func SelectPlaylists(playlists []Playlist, formats []string) []Playlist {
	selected := make([]Playlist, 0, len(formats))
	for _, format := range formats {
		if pl, ok := selectBestQuality(playlists, format); ok {
			selected = append(selected, pl)
		}
	}
	return selected
}

// selectBestQuality returns the best-quality playlist of the given format
// (highest > high > low > unknown), or false if the format is absent.
func selectBestQuality(playlists []Playlist, format string) (Playlist, bool) {
	var best Playlist
	found := false
	// The seed must exceed the unknown-quality rank below, or a channel
	// whose playlists all have unrecognized quality labels would select
	// nothing at all instead of falling back to its first entry.
	bestRank := len(qualityRank) + 1
	for _, playlist := range playlists {
		if playlist.Format != format {
			continue
		}
		rank, ok := qualityRank[playlist.Quality]
		if !ok {
			rank = len(qualityRank)
		}
		if rank < bestRank {
			best = playlist
			bestRank = rank
			found = true
		}
	}
	return best, found
}
