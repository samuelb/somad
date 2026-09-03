package server

import (
	"errors"
	"sync"
	"testing"
	"time"

	"somad/internal/audio"
	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeScrobbler is a race-safe test double for the Scrobbler interface. It
// can be told to fail the first N calls of either kind, to exercise the
// bounded-retry behavior in lastfm.go.
type fakeScrobbler struct {
	mu sync.Mutex

	nowPlayingCalls []trackCall
	scrobbleCalls   []scrobbleCall
	sessionKeys     []string

	failNowPlayingTimes int
	failScrobbleTimes   int
}

type trackCall struct{ artist, title string }
type scrobbleCall struct {
	artist, title string
	startedAt     time.Time
}

func (f *fakeScrobbler) UpdateNowPlaying(artist, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nowPlayingCalls = append(f.nowPlayingCalls, trackCall{artist, title})
	if f.failNowPlayingTimes > 0 {
		f.failNowPlayingTimes--
		return errors.New("now-playing failed")
	}
	return nil
}

func (f *fakeScrobbler) Scrobble(artist, title string, startedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scrobbleCalls = append(f.scrobbleCalls, scrobbleCall{artist, title, startedAt})
	if f.failScrobbleTimes > 0 {
		f.failScrobbleTimes--
		return errors.New("scrobble failed")
	}
	return nil
}

func (f *fakeScrobbler) SetSessionKey(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionKeys = append(f.sessionKeys, key)
}

func (f *fakeScrobbler) nowPlayingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.nowPlayingCalls)
}

// lastNowPlaying returns the most recent UpdateNowPlaying call. Callers
// check nowPlayingCount() first (typically via require.Eventually), so
// this never runs against an empty slice.
func (f *fakeScrobbler) lastNowPlaying() trackCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nowPlayingCalls[len(f.nowPlayingCalls)-1]
}

func (f *fakeScrobbler) scrobbleCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.scrobbleCalls)
}

func (f *fakeScrobbler) lastScrobble() scrobbleCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scrobbleCalls[len(f.scrobbleCalls)-1]
}

// shrinkLastfmThresholds shrinks the minimum-play-duration and retry-delay
// package vars for the duration of t, so tests run in milliseconds instead
// of tens of seconds.
func shrinkLastfmThresholds(t *testing.T) {
	t.Helper()
	prevMin, prevRetry := lastfmMinPlayDuration, lastfmRetryDelay
	lastfmMinPlayDuration = 20 * time.Millisecond
	lastfmRetryDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		lastfmMinPlayDuration = prevMin
		lastfmRetryDelay = prevRetry
	})
}

func TestLastfm_UpdateNowPlayingOnTrackChange(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}

	c.waitState("track title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 1 }, 2*time.Second, 5*time.Millisecond)
	got := scrobbler.lastNowPlaying()
	assert.Equal(t, "Boards of Canada", got.artist)
	assert.Equal(t, "Dayvan Cowboy", got.title)
}

func TestLastfm_SkipsTitlesWithNoArtist(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Ambient Soundscape", Gen: player.currentGen()}

	c.waitState("track title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Ambient Soundscape"
	})

	// Give any (incorrect) async call a chance to land before asserting none did.
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, scrobbler.nowPlayingCount(), "a title with no artist must never be sent to last.fm")
}

func TestLastfm_ScrobblesPreviousTrackOnNextTitleChangeWhenLongEnough(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("first title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	// Outlast lastfmMinPlayDuration before the next title arrives.
	time.Sleep(2 * lastfmMinPlayDuration)

	player.trackChan <- audio.TrackInfo{Title: "Tycho - A Walk", Gen: player.currentGen()}
	c.waitState("second title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Tycho - A Walk"
	})

	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 1 }, 2*time.Second, 5*time.Millisecond)
	got := scrobbler.lastScrobble()
	assert.Equal(t, "Boards of Canada", got.artist)
	assert.Equal(t, "Dayvan Cowboy", got.title)

	// The second (still-playing) track gets a now-playing update but is not
	// itself scrobbled yet.
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
}

func TestLastfm_DoesNotScrobbleWhenPlayedTooShort(t *testing.T) {
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	// lastfmMinPlayDuration keeps its real (long) default in this test, so
	// a near-instant title change never crosses it.
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("first title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	player.trackChan <- audio.TrackInfo{Title: "Tycho - A Walk", Gen: player.currentGen()}
	c.waitState("second title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Tycho - A Walk"
	})

	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, scrobbler.scrobbleCount(), "a track played for under the minimum must not be scrobbled")
}

func TestLastfm_ScrobblesOnStop(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))

	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 1 }, 2*time.Second, 5*time.Millisecond)
	got := scrobbler.lastScrobble()
	assert.Equal(t, "Boards of Canada", got.artist)
	assert.Equal(t, "Dayvan Cowboy", got.title)
}

func TestLastfm_ScrobblesOnChannelSwitch(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))

	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 1 }, 2*time.Second, 5*time.Millisecond)
	got := scrobbler.lastScrobble()
	assert.Equal(t, "Boards of Canada", got.artist)
	assert.Equal(t, "Dayvan Cowboy", got.title)
}

func TestLastfm_RetriesOnceThenSucceeds(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{failNowPlayingTimes: 1}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	// One failed call, then one retry after lastfmRetryDelay that succeeds.
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, scrobbler.nowPlayingCount(), "must retry exactly once, not keep retrying")
}

func TestLastfm_GivesUpAfterOneRetry(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{failNowPlayingTimes: 2}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, scrobbler.nowPlayingCount(), "must not retry a third time")
}

func TestLastfm_DisabledWhenNoScrobblerConfigured(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}

	// Just needs to not panic or block; there is nothing to assert on a nil
	// scrobbler beyond "the usual state broadcast still happens".
	c.waitState("title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})
}

func TestReloadLastfm_AppliesFreshSessionKey(t *testing.T) {
	scrobbler := &fakeScrobbler{}
	s, _ := newTestServer(t, Config{
		Scrobbler:           scrobbler,
		ReloadLastfmSession: func() (string, error) { return "fresh-key", nil },
	})

	require.NoError(t, s.ReloadLastfm())

	scrobbler.mu.Lock()
	defer scrobbler.mu.Unlock()
	require.Len(t, scrobbler.sessionKeys, 1)
	assert.Equal(t, "fresh-key", scrobbler.sessionKeys[0])
}

func TestReloadLastfm_NoScrobblerIsANoOp(t *testing.T) {
	called := false
	s, _ := newTestServer(t, Config{
		ReloadLastfmSession: func() (string, error) { called = true; return "x", nil },
	})

	require.NoError(t, s.ReloadLastfm())
	assert.False(t, called, "reload must not run when scrobbling is not configured")
}

func TestReloadLastfm_PropagatesResolveError(t *testing.T) {
	scrobbler := &fakeScrobbler{}
	s, _ := newTestServer(t, Config{
		Scrobbler:           scrobbler,
		ReloadLastfmSession: func() (string, error) { return "", errors.New("read failed") },
	})

	err := s.ReloadLastfm()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read failed")
}

func TestReloadLastfm_RPC(t *testing.T) {
	scrobbler := &fakeScrobbler{}
	s, _ := newTestServer(t, Config{
		Scrobbler:           scrobbler,
		ReloadLastfmSession: func() (string, error) { return "via-rpc", nil },
	})
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodReloadLastfm, nil)
	require.Empty(t, resp.Error)

	scrobbler.mu.Lock()
	defer scrobbler.mu.Unlock()
	require.Len(t, scrobbler.sessionKeys, 1)
	assert.Equal(t, "via-rpc", scrobbler.sessionKeys[0])
}
