package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"somad/internal/audio"
	"somad/internal/protocol"
	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSongsServer points songsURLFormat at an httptest server that answers
// with the given songs.json body (or, when handler is non-nil, runs it
// instead), restoring the original value and allowlisting the test host for
// the duration of t.
func withSongsServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	securitytest.AllowTestHosts(t)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	prev := songsURLFormat
	songsURLFormat = ts.URL + "/%s.json"
	t.Cleanup(func() { songsURLFormat = prev })
	return ts
}

func TestHistory_RecordsNewestFirst(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "First Track", Gen: player.currentGen()}
	c.waitState("first title", func(st protocol.PlaybackState) bool { return st.TrackTitle == "First Track" })
	player.trackChan <- audio.TrackInfo{Title: "Second Track", Gen: player.currentGen()}
	c.waitState("second title", func(st protocol.PlaybackState) bool { return st.TrackTitle == "Second Track" })

	resp := c.call(protocol.MethodHistory, protocol.HistoryParams{})
	require.Empty(t, resp.Error)
	var result protocol.HistoryResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))

	require.Len(t, result.Entries, 2)
	assert.Equal(t, "Second Track", result.Entries[0].Title, "newest entry first")
	assert.Equal(t, "First Track", result.Entries[1].Title)
	assert.Equal(t, "groovesalad", result.Entries[0].ChannelID)
	assert.Equal(t, "Groove Salad", result.Entries[0].ChannelTitle)
	assert.WithinDuration(t, time.Now(), result.Entries[0].Time, 5*time.Second)
}

func TestHistory_SkipsDuplicateOfLastEntry(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Same Track", Gen: player.currentGen()}
	c.waitState("title set", func(st protocol.PlaybackState) bool { return st.TrackTitle == "Same Track" })

	// Force the deduplicated-by-the-decoder edge case: record the identical
	// title directly, bypassing the demuxer's own change detection.
	s.mu.Lock()
	s.recordHistoryLocked(s.channelID, s.channelTitle, "Same Track")
	s.recordHistoryLocked(s.channelID, s.channelTitle, "Same Track")
	entries := len(s.history)
	s.mu.Unlock()

	assert.Equal(t, 1, entries, "a repeated title must not be recorded twice in a row")
}

func TestHistory_FiltersByChannel(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Groove Track", Gen: player.currentGen()}
	c.waitState("groove title", func(st protocol.PlaybackState) bool { return st.TrackTitle == "Groove Track" })

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))
	player.trackChan <- audio.TrackInfo{Title: "Drone Track", Gen: player.currentGen()}
	c.waitState("drone title", func(st protocol.PlaybackState) bool { return st.TrackTitle == "Drone Track" })

	resp := c.call(protocol.MethodHistory, protocol.HistoryParams{ChannelID: "groovesalad"})
	require.Empty(t, resp.Error)
	var result protocol.HistoryResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))

	require.Len(t, result.Entries, 1)
	assert.Equal(t, "Groove Track", result.Entries[0].Title)
	assert.Equal(t, "groovesalad", result.Entries[0].ChannelID)
}

func TestHistory_LimitClampsToDefaultAndMax(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	for i := 0; i < 3; i++ {
		title := fmt.Sprintf("Track %d", i)
		player.trackChan <- audio.TrackInfo{Title: title, Gen: player.currentGen()}
		c.waitState(title, func(st protocol.PlaybackState) bool { return st.TrackTitle == title })
	}

	// A zero/negative limit and an oversized one both fall back to the ring
	// size, so both requests below return every recorded entry.
	for _, limit := range []int{0, 1000} {
		resp := c.call(protocol.MethodHistory, protocol.HistoryParams{Limit: limit})
		require.Empty(t, resp.Error)
		var result protocol.HistoryResult
		require.NoError(t, json.Unmarshal(resp.Result, &result))
		assert.Len(t, result.Entries, 3)
	}

	resp := c.call(protocol.MethodHistory, protocol.HistoryParams{Limit: 2})
	require.Empty(t, resp.Error)
	var result protocol.HistoryResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Len(t, result.Entries, 2)
	assert.Equal(t, "Track 2", result.Entries[0].Title)
}

func TestHistory_RingDropsOldestBeyondCap(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	prevSize := historyRingSize
	historyRingSize = 2
	t.Cleanup(func() { historyRingSize = prevSize })

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "One")
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Two")
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Three")
	s.mu.Unlock()

	entries := s.History("", 0)
	require.Len(t, entries, 2)
	assert.Equal(t, "Three", entries[0].Title)
	assert.Equal(t, "Two", entries[1].Title, "the oldest entry is evicted once the ring is full")
}

func TestHistory_BackfillMergesAndDeduplicates(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	withSongsServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"songs":[
			{"artist":"Boards of Canada","title":"Roygbiv","date":"1000000000"},
			{"artist":"Boards of Canada","title":"Live Track","date":"999999000"}
		]}`))
	})

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Boards of Canada - Live Track")
	s.mu.Unlock()

	entries := s.History("groovesalad", 10)

	require.Len(t, entries, 2, "the live entry and the one new backfilled entry, deduplicated by title")
	titles := []string{entries[0].Title, entries[1].Title}
	assert.Contains(t, titles, "Boards of Canada - Live Track")
	assert.Contains(t, titles, "Boards of Canada - Roygbiv")
	for _, e := range entries {
		assert.Equal(t, "groovesalad", e.ChannelID)
		assert.Equal(t, "Groove Salad", e.ChannelTitle, "a backfilled entry inherits the channel title")
	}
}

func TestHistory_BackfillFailureIsBestEffort(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	withSongsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Live Track")
	s.mu.Unlock()

	entries := s.History("groovesalad", 10)

	require.Len(t, entries, 1, "a failed backfill must not fail the request or lose the live entry")
	assert.Equal(t, "Live Track", entries[0].Title)
}

func TestHistory_BackfillNotAttemptedWhenRingAlreadySatisfiesLimit(t *testing.T) {
	var hits atomic.Int32
	s, _ := newTestServer(t, Config{})
	withSongsServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"songs":[]}`))
	})

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Only Track")
	s.mu.Unlock()

	entries := s.History("groovesalad", 1)

	require.Len(t, entries, 1)
	assert.Zero(t, hits.Load(), "the ring already has enough entries; no network fetch is needed")
}

func TestHistory_SongsResponseCached(t *testing.T) {
	var hits atomic.Int32
	s, _ := newTestServer(t, Config{})
	withSongsServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"songs":[{"artist":"A","title":"B","date":"1000000000"}]}`))
	})

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Live Track")
	s.mu.Unlock()

	s.History("groovesalad", 10)
	s.History("groovesalad", 10)

	assert.EqualValues(t, 1, hits.Load(), "a cached response must not be refetched within the TTL")
}

func TestHistory_SongsResponseRefetchedAfterTTL(t *testing.T) {
	var hits atomic.Int32
	s, _ := newTestServer(t, Config{})
	withSongsServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"songs":[{"artist":"A","title":"B","date":"1000000000"}]}`))
	})
	prevTTL := songsCacheTTL
	songsCacheTTL = 0
	t.Cleanup(func() { songsCacheTTL = prevTTL })

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Live Track")
	s.mu.Unlock()

	s.History("groovesalad", 10)
	s.History("groovesalad", 10)

	assert.GreaterOrEqual(t, hits.Load(), int32(2), "an expired cache entry must be refetched")
}

func TestHistory_NoChannelIDNeverBackfills(t *testing.T) {
	var hits atomic.Int32
	s, _ := newTestServer(t, Config{})
	withSongsServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	})

	s.mu.Lock()
	s.recordHistoryLocked("groovesalad", "Groove Salad", "Live Track")
	s.mu.Unlock()

	entries := s.History("", 10)

	require.Len(t, entries, 1)
	assert.Zero(t, hits.Load(), "without a channel filter there is nothing to backfill")
}
