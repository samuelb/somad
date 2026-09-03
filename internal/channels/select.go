package channels

import "strings"

// qualityRank orders SomaFM playlist quality levels, best first. These are
// also the valid values of the server.quality config key and the --quality
// daemon flag (internal/config.ServerConfig).
var qualityRank = map[string]int{"highest": 0, "high": 1, "low": 2}

// SelectPlaylists returns the playlists to try, in playback-preference
// order: for each format in formats (most preferred first, e.g. AAC before
// MP3 where the platform decodes it) the playlist of that format closest to
// the preferred quality. quality is one of the qualityRank keys ("highest",
// "high", "low"); any other value, including "", means no preference and
// selects the best available quality, matching the pre-quality-knob
// behavior. The caller works through the result until one connects, so a
// format whose stream fails still falls back to the next.
func SelectPlaylists(playlists []Playlist, formats []string, quality string) []Playlist {
	rank, ok := qualityRank[quality]
	if !ok {
		rank = 0 // no preference, or an unrecognized value: prefer the best quality
	}
	selected := make([]Playlist, 0, len(formats))
	for _, format := range formats {
		if pl, ok := selectQuality(playlists, format, rank); ok {
			selected = append(selected, pl)
		}
	}
	return selected
}

// selectQuality returns the playlist of the given format whose quality is
// closest to preferredRank (0 = highest), or false if the format is absent.
// A channel lacking the exact preferred quality falls back to the nearest
// one available; ties go to the better (lower-rank) quality, then to an
// https playlist URL over a plain-http one.
func selectQuality(playlists []Playlist, format string, preferredRank int) (Playlist, bool) {
	var best Playlist
	found := false
	var bestDist, bestRank int
	for _, playlist := range playlists {
		if playlist.Format != format {
			continue
		}
		rank, ok := qualityRank[playlist.Quality]
		if !ok {
			rank = len(qualityRank)
		}
		dist := rank - preferredRank
		if dist < 0 {
			dist = -dist
		}
		switch {
		case !found:
			best, bestDist, bestRank, found = playlist, dist, rank, true
		case dist < bestDist, dist == bestDist && rank < bestRank:
			best, bestDist, bestRank = playlist, dist, rank
		case dist == bestDist && rank == bestRank && isHTTPSURL(playlist.URL) && !isHTTPSURL(best.URL):
			best = playlist
		}
	}
	return best, found
}

// isHTTPSURL reports whether url starts with an https scheme, matched
// case-insensitively since playlist URLs are not always spec-exact.
func isHTTPSURL(url string) bool {
	const scheme = "https://"
	return len(url) >= len(scheme) && strings.EqualFold(url[:len(scheme)], scheme)
}
