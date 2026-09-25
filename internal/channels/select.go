package channels

import (
	"cmp"
	"slices"

	"somad/internal/security"
)

// qualityRank orders SomaFM playlist quality levels, best first. These are
// also the valid values of the server.quality config key and the --quality
// daemon flag (internal/config.ServerConfig).
var qualityRank = map[string]int{"highest": 0, "high": 1, "low": 2}

// SelectPlaylists returns the playlists to try, in playback-preference
// order: for each format in formats (most preferred first, e.g. AAC before
// MP3 where the platform decodes it) the playlist of that format closest to
// the preferred quality, ordered by that closeness first and by format
// preference only among equally close ones. So a quality one format lacks
// comes from another format that has it rather than from the nearest
// quality of the preferred format: SomaFM offers "high" and "low" only as
// HE-AAC ("aacp"), and its AAC and MP3 playlists only at "highest". quality
// is one of the qualityRank keys ("highest", "high", "low"); any other
// value, including "", means no preference and selects the best available
// quality, matching the pre-quality-knob behavior. The caller works through
// the result until one connects, so a format whose stream fails still
// falls back to the next.
func SelectPlaylists(playlists []Playlist, formats []string, quality string) []Playlist {
	preferred, ok := qualityRank[quality]
	if !ok {
		preferred = 0 // no preference, or an unrecognized value: prefer the best quality
	}
	type candidate struct {
		playlist   Playlist
		dist, rank int
	}
	candidates := make([]candidate, 0, len(formats))
	for _, format := range formats {
		if pl, ok := selectQuality(playlists, format, preferred); ok {
			rank := playlistRank(pl)
			candidates = append(candidates, candidate{pl, distance(rank, preferred), rank})
		}
	}
	// Stable, so equally close and equally good candidates keep the format
	// preference order.
	slices.SortStableFunc(candidates, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(a.dist, b.dist), cmp.Compare(a.rank, b.rank))
	})
	selected := make([]Playlist, len(candidates))
	for i, c := range candidates {
		selected[i] = c.playlist
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
		rank := playlistRank(playlist)
		dist := distance(rank, preferredRank)
		switch {
		case !found:
			best, bestDist, bestRank, found = playlist, dist, rank, true
		case dist < bestDist, dist == bestDist && rank < bestRank:
			best, bestDist, bestRank = playlist, dist, rank
		case dist == bestDist && rank == bestRank && security.IsHTTPSURL(playlist.URL) && !security.IsHTTPSURL(best.URL):
			best = playlist
		}
	}
	return best, found
}

// playlistRank is the playlist's qualityRank, with unrecognized labels
// ranked below every known one.
func playlistRank(pl Playlist) int {
	if rank, ok := qualityRank[pl.Quality]; ok {
		return rank
	}
	return len(qualityRank)
}

// distance is how far a quality rank is from the preferred one.
func distance(rank, preferred int) int {
	if rank < preferred {
		return preferred - rank
	}
	return rank - preferred
}
