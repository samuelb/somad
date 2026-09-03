package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/security"
)

// historyRingSize caps the in-memory now-playing history the daemon keeps
// across its lifetime; older entries are dropped as new ones arrive. It also
// doubles as the default and maximum number of entries a history request
// returns. A variable so tests can shrink it.
var historyRingSize = 50

// historyEntry is one now-playing title change, timestamped when the daemon
// observed it (or, for a backfilled entry, when SomaFM's own history
// reported it).
type historyEntry struct {
	channelID    string
	channelTitle string
	title        string
	time         time.Time
}

// recordHistoryLocked appends a title change to the ring, skipping an exact
// repeat of the last entry (ICY metadata sometimes resends the same title,
// e.g. across a reconnect). Caller holds s.mu.
func (s *Server) recordHistoryLocked(channelID, channelTitle, title string) {
	if title == "" {
		return
	}
	if n := len(s.history); n > 0 {
		last := s.history[n-1]
		if last.channelID == channelID && last.title == title {
			return
		}
	}
	s.history = append(s.history, historyEntry{
		channelID:    channelID,
		channelTitle: channelTitle,
		title:        title,
		time:         time.Now(),
	})
	if len(s.history) > historyRingSize {
		s.history = s.history[len(s.history)-historyRingSize:]
	}
}

// History returns recent now-playing titles, newest first. With channelID
// set, the result is filtered to that channel and, when the in-memory ring
// has too few entries for it and channelID names a channel in the current
// catalog, backfilled (best-effort) from SomaFM's own song history; an
// unrecognized channelID (never validated against the catalog by the wire
// protocol) gets the ring-only result, with no backfill attempt at all —
// there is no known channel to fetch or cache song history for. limit <= 0,
// or greater than historyRingSize, is clamped to historyRingSize.
func (s *Server) History(channelID string, limit int) []protocol.HistoryEntry {
	if limit <= 0 || limit > historyRingSize {
		limit = historyRingSize
	}

	s.mu.Lock()
	entries := make([]historyEntry, len(s.history))
	copy(entries, s.history)
	var channelTitle string
	knownChannel := true
	if channelID != "" {
		var ok bool
		var ch channels.Channel
		if ch, ok = s.findChannelLocked(channelID); ok {
			channelTitle = ch.Title
		}
		knownChannel = ok
	}
	s.mu.Unlock()

	// Newest first.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	if channelID != "" {
		filtered := make([]historyEntry, 0, len(entries))
		for _, e := range entries {
			if e.channelID == channelID {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
		if knownChannel && len(entries) < limit {
			entries = s.backfillHistory(channelID, entries)
		}
	}

	if len(entries) > limit {
		entries = entries[:limit]
	}

	result := make([]protocol.HistoryEntry, len(entries))
	for i, e := range entries {
		title := e.channelTitle
		if title == "" {
			title = channelTitle
		}
		result[i] = protocol.HistoryEntry{
			ChannelID:    e.channelID,
			ChannelTitle: title,
			Title:        e.title,
			Time:         e.time,
		}
	}
	return result
}

// backfillHistory merges existing (already filtered to one channel, newest
// first) with that channel's cached SomaFM song history, de-duplicated by
// title and re-sorted newest first. The fetch is best-effort: any failure
// (network, format, allowlist) simply leaves existing unchanged, never
// failing the history RPC.
func (s *Server) backfillHistory(channelID string, existing []historyEntry) []historyEntry {
	fetched := s.songsCached(channelID)
	if len(fetched) == 0 {
		return existing
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[e.title] = true
	}
	merged := make([]historyEntry, len(existing), len(existing)+len(fetched))
	copy(merged, existing)
	for _, e := range fetched {
		if seen[e.title] {
			continue
		}
		seen[e.title] = true
		merged = append(merged, e)
	}
	sort.SliceStable(merged, func(i, j int) bool { return merged[i].time.After(merged[j].time) })
	return merged
}

// songsCacheEntry is one channel's cached SomaFM song history.
type songsCacheEntry struct {
	fetchedAt time.Time
	entries   []historyEntry
}

// songsCacheTTL bounds how long a fetched songs.json response is reused
// before the next history request for that channel refetches it. A
// variable so tests can shrink it.
var songsCacheTTL = 5 * time.Minute

// songsCached returns channelID's recent song history from SomaFM,
// fetching (and caching) it on a miss or an expired entry. A fetch failure
// returns nil; it is logged, not propagated, so a flaky or unreachable
// SomaFM never fails the history RPC.
func (s *Server) songsCached(channelID string) []historyEntry {
	s.songsCacheMu.Lock()
	if c, ok := s.songsCache[channelID]; ok && time.Since(c.fetchedAt) < songsCacheTTL {
		entries := c.entries
		s.songsCacheMu.Unlock()
		return entries
	}
	s.songsCacheMu.Unlock()

	entries, err := fetchSongs(channelID, s.userAgent)
	if err != nil {
		log.Printf("history: fetching song history for %s failed: %v", channelID, err)
		entries = nil
	}

	s.songsCacheMu.Lock()
	if s.songsCache == nil {
		s.songsCache = make(map[string]songsCacheEntry)
	}
	s.songsCache[channelID] = songsCacheEntry{fetchedAt: time.Now(), entries: entries}
	s.songsCacheMu.Unlock()
	return entries
}

// songsURLFormat is the endpoint for a channel's recent song history, with
// the channel ID substituted in. A variable so tests can point it at an
// httptest server.
var songsURLFormat = "https://somafm.com/songs/%s.json"

// maxSongsBytes caps the songs.json download, matching the playlist cap
// (ADR-0010): this response is small and any larger body is not something a
// well-behaved server would send.
const maxSongsBytes = 1 << 20 // 1 MiB

// songsResponse is SomaFM's songs.json shape, parsed permissively: only the
// fields this needs are declared, and a song missing them is skipped rather
// than failing the whole fetch.
type songsResponse struct {
	Songs []struct {
		Artist string `json:"artist"`
		Title  string `json:"title"`
		// Date is a Unix timestamp in seconds, encoded as a string.
		Date string `json:"date"`
	} `json:"songs"`
}

// fetchSongs fetches and parses channelID's recent song history from
// SomaFM. The title fields feed the same "Artist - Title" shape the ICY
// metadata produces, so backfilled and live entries render the same way.
// channelID is path-escaped before it reaches the URL — defense in depth,
// since History only ever calls this for a channelID already found in the
// current catalog (see backfillHistory), never an arbitrary client string.
func fetchSongs(channelID, userAgent string) ([]historyEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf(songsURLFormat, url.PathEscape(channelID))
	req, err := security.NewRequest(ctx, reqURL, userAgent)
	if err != nil {
		return nil, fmt.Errorf("invalid songs URL: %w", err)
	}

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch song history: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from song history: %d", resp.StatusCode)
	}

	var parsed songsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSongsBytes)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("failed to decode song history: %w", err)
	}

	entries := make([]historyEntry, 0, len(parsed.Songs))
	for _, song := range parsed.Songs {
		title := song.Title
		if song.Artist != "" && song.Title != "" {
			title = song.Artist + " - " + song.Title
		}
		if title == "" {
			continue
		}
		var t time.Time
		if sec, err := strconv.ParseInt(song.Date, 10, 64); err == nil {
			t = time.Unix(sec, 0)
		}
		entries = append(entries, historyEntry{channelID: channelID, title: title, time: t})
	}
	return entries, nil
}
