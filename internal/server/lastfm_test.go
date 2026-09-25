package server

import (
	"context"
	"errors"
	"net"
	"net/http"
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

	// scrobbleDelay, when set, makes Scrobble sleep this long before
	// returning, to exercise Shutdown's bounded wait for a slow Last.fm.
	scrobbleDelay time.Duration
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
	delay := f.scrobbleDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
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

// playScrobbled starts a server scrobbling to scrobbler, connects a client,
// plays groovesalad, and delivers title as its first now-playing title.
func playScrobbled(t *testing.T, scrobbler *fakeScrobbler, title string) (*Server, *mockPlayer, *tclient) {
	t.Helper()
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	pushTitle(t, player, c, title)
	return s, player, c
}

// pushTitle delivers title as the playing session's ICY title and waits
// until the server reports it.
func pushTitle(t *testing.T, player *mockPlayer, c *tclient, title string) {
	t.Helper()
	player.trackChan <- audio.TrackInfo{Title: title, Gen: player.currentGen()}
	c.waitState(title, func(st protocol.PlaybackState) bool { return st.TrackTitle == title })
}

// awaitScrobble waits for the first scrobble and checks it is artist/title.
func awaitScrobble(t *testing.T, scrobbler *fakeScrobbler, artist, title string) {
	t.Helper()
	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 1 }, 2*time.Second, 5*time.Millisecond)
	got := scrobbler.lastScrobble()
	assert.Equal(t, artist, got.artist)
	assert.Equal(t, title, got.title)
}

func TestLastfm_UpdateNowPlayingOnTrackChange(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 1 }, 2*time.Second, 5*time.Millisecond)
	got := scrobbler.lastNowPlaying()
	assert.Equal(t, "Boards of Canada", got.artist)
	assert.Equal(t, "Dayvan Cowboy", got.title)
}

func TestLastfm_SkipsTitlesWithNoArtist(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	playScrobbled(t, scrobbler, "Ambient Soundscape")

	// Give any (incorrect) async call a chance to land before asserting none did.
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, scrobbler.nowPlayingCount(), "a title with no artist must never be sent to last.fm")
}

func TestLastfm_ScrobblesPreviousTrackOnNextTitleChangeWhenLongEnough(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	// Outlast lastfmMinPlayDuration before the next title arrives.
	time.Sleep(2 * lastfmMinPlayDuration)

	pushTitle(t, player, c, "Tycho - A Walk")

	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")

	// The second (still-playing) track gets a now-playing update but is not
	// itself scrobbled yet.
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
}

func TestLastfm_DoesNotScrobbleWhenPlayedTooShort(t *testing.T) {
	// lastfmMinPlayDuration keeps its real (long) default in this test, so
	// a near-instant title change never crosses it.
	scrobbler := &fakeScrobbler{}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	pushTitle(t, player, c, "Tycho - A Walk")

	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, scrobbler.scrobbleCount(), "a track played for under the minimum must not be scrobbled")
}

func TestLastfm_ScrobblesOnStop(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	_, _, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))

	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")
}

func TestLastfm_ScrobblesOnChannelSwitch(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	_, _, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))

	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")
}

func TestLastfm_RetriesOnceThenSucceeds(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{failNowPlayingTimes: 1}
	playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	// One failed call, then one retry after lastfmRetryDelay that succeeds.
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, scrobbler.nowPlayingCount(), "must retry exactly once, not keep retrying")
}

func TestLastfm_GivesUpAfterOneRetry(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{failNowPlayingTimes: 2}
	playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, scrobbler.nowPlayingCount(), "must not retry a third time")
}

func TestLastfm_SkipsNowPlayingRetryWhenTrackChanged(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{failNowPlayingTimes: 1}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	// A second title arrives before the first now-playing update's retry
	// delay elapses; it supersedes the first as s.lastfmTrack.
	pushTitle(t, player, c, "Tycho - A Walk")
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)

	// Give the first track's (should-be-skipped) retry time to fire if the
	// latest-wins check were not in place.
	time.Sleep(3 * lastfmRetryDelay)
	assert.Equal(t, 2, scrobbler.nowPlayingCount(),
		"the stale retry for the superseded first track must be skipped")
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

// TestReloadLastfm_StartsScrobblingWhenConfiguredLater covers api_key and
// api_secret being added to the config after the daemon started: "soma
// lastfm login" then reloads a daemon that has no scrobbler at all, and it
// must start scrobbling rather than silently ignore the login.
func TestReloadLastfm_StartsScrobblingWhenConfiguredLater(t *testing.T) {
	scrobbler := &fakeScrobbler{}
	s, player := newTestServer(t, Config{
		ReloadLastfmSession: func() (string, error) { return "fresh-key", nil },
		LoadScrobbler:       func() (Scrobbler, error) { return scrobbler, nil },
	})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodReloadLastfm, nil)
	require.Empty(t, resp.Error)
	scrobbler.mu.Lock()
	assert.Equal(t, []string{"fresh-key"}, scrobbler.sessionKeys)
	scrobbler.mu.Unlock()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 1 }, 2*time.Second, 5*time.Millisecond)

	// A later reload (a logout, say) reuses it rather than building another.
	require.NoError(t, s.ReloadLastfm())
	scrobbler.mu.Lock()
	assert.Len(t, scrobbler.sessionKeys, 2)
	scrobbler.mu.Unlock()
}

func TestReloadLastfm_StillUnconfiguredIsANoOp(t *testing.T) {
	resolved := false
	s, _ := newTestServer(t, Config{
		ReloadLastfmSession: func() (string, error) { resolved = true; return "x", nil },
		LoadScrobbler:       func() (Scrobbler, error) { return nil, nil },
	})

	require.NoError(t, s.ReloadLastfm())
	assert.False(t, resolved, "no session to apply without a scrobbler")
	s.mu.Lock()
	assert.Nil(t, s.scrobbler)
	s.mu.Unlock()
}

func TestReloadLastfm_ReportsLoadError(t *testing.T) {
	s, _ := newTestServer(t, Config{
		LoadScrobbler: func() (Scrobbler, error) { return nil, errors.New("error loading config: bad yaml") },
	})
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodReloadLastfm, nil)
	assert.Contains(t, resp.Error, "bad yaml")
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

func TestShutdown_ScrobblesFinalTrack(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	s, _, _ := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)

	s.Shutdown()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}

	require.Equal(t, 1, scrobbler.scrobbleCount(), "Shutdown must scrobble the track that was playing, like Stop does")
	got := scrobbler.lastScrobble()
	assert.Equal(t, "Boards of Canada", got.artist)
	assert.Equal(t, "Dayvan Cowboy", got.title)
}

// TestRun_ReturnsOnlyAfterShutdownCompletes covers the daemon's exit path:
// the process exits as soon as Run returns, so Run must not return while a
// Shutdown started elsewhere (a signal, the idle timer, a client) is still
// waiting for the final scrobble to be sent.
func TestRun_ReturnsOnlyAfterShutdownCompletes(t *testing.T) {
	shrinkLastfmThresholds(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Run refreshes the catalog; a failure keeps the seeded one.
	fetched := make(chan struct{}, 1)
	stubChannelsNetwork(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		select {
		case fetched <- struct{}{}:
		default:
		}
	})
	// Run's refresh goroutine is not joined by Shutdown; waiting for its
	// request orders its read of the stubbed URL before the cleanup that
	// restores it.
	t.Cleanup(func() {
		select {
		case <-fetched:
		case <-time.After(5 * time.Second):
		}
	})

	scrobbler := &fakeScrobbler{scrobbleDelay: 300 * time.Millisecond}
	s, player := newTestServer(t, Config{Scrobbler: scrobbler})
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ln) }()

	c := connect(t, s)
	c.hello()
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)

	go s.Shutdown() // as the signal handler does
	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Shutdown")
	}
	assert.Equal(t, 1, scrobbler.scrobbleCount(),
		"Run must not return before Shutdown has sent the final scrobble")
}

func TestShutdown_LastfmWaitIsBounded(t *testing.T) {
	shrinkLastfmThresholds(t)
	prevWait := lastfmShutdownWait
	lastfmShutdownWait = 50 * time.Millisecond
	t.Cleanup(func() { lastfmShutdownWait = prevWait })

	scrobbler := &fakeScrobbler{scrobbleDelay: 2 * time.Second}
	s, _, _ := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)

	start := time.Now()
	s.Shutdown()
	elapsed := time.Since(start)

	assert.Less(t, elapsed, time.Second,
		"shutdown must not wait for a slow scrobble beyond lastfmShutdownWait")
}

func TestSubmitLastfm_SkipsRetryOnceClosing(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{failScrobbleTimes: 1}
	s, _, _ := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)

	s.Shutdown()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}

	// The one (failing) scrobble attempt happens as part of Shutdown's
	// bounded wait; the 10s retry must be skipped once closing, not merely
	// cut off by the wait bound.
	time.Sleep(3 * lastfmRetryDelay)
	assert.Equal(t, 1, scrobbler.scrobbleCount(), "no retry once the server is shutting down")
}

// shrinkLastfmSameTrackWindow shrinks lastfmSameTrackWindow for the
// duration of t, so a re-appearing title counts as a new play at once.
func shrinkLastfmSameTrackWindow(t *testing.T, d time.Duration) {
	t.Helper()
	prev := lastfmSameTrackWindow
	lastfmSameTrackWindow = d
	t.Cleanup(func() { lastfmSameTrackWindow = prev })
}

// assertScrobbleCountStays asserts that no further scrobble lands within a
// grace period after any (incorrect) async one would have.
func assertScrobbleCountStays(t *testing.T, scrobbler *fakeScrobbler, want int) {
	t.Helper()
	time.Sleep(2*lastfmMinPlayDuration + 50*time.Millisecond)
	assert.Equal(t, want, scrobbler.scrobbleCount(), "the same play must be scrobbled at most once")
}

func TestLastfm_StreamDropAndReconnectDoesNotScrobbleTwice(t *testing.T) {
	shrinkLastfmThresholds(t)
	prev := reconnectBaseDelay
	reconnectBaseDelay = time.Millisecond
	t.Cleanup(func() { reconnectBaseDelay = prev })

	scrobbler := &fakeScrobbler{}
	s, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")
	go s.watchPlayerErrors()

	time.Sleep(2 * lastfmMinPlayDuration)

	// The stream drops mid-track and the daemon reconnects; the ended play
	// qualifies and is scrobbled once.
	player.errChan <- errors.New("stream read error")
	c.waitState("reconnecting", func(st protocol.PlaybackState) bool { return st.Status == protocol.StatusReconnecting })
	c.waitState("recovered", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.TrackTitle == ""
	})
	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")
	first := scrobbler.lastScrobble()

	// The fresh connection re-reports the same title: same play, resumed.
	// It is still announced as now playing, but when it finally ends it
	// must not be scrobbled a second time.
	pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	require.Eventually(t, func() bool { return scrobbler.nowPlayingCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(2 * lastfmMinPlayDuration)
	pushTitle(t, player, c, "Tycho - A Walk")

	assertScrobbleCountStays(t, scrobbler, 1)
	assert.Equal(t, first, scrobbler.lastScrobble())
}

func TestLastfm_PauseAndUnpauseDoesNotScrobbleTwice(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))
	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")

	// Unpause: live radio is still on the same track, which the new
	// connection re-reports.
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	c.waitState("replaying", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.TrackTitle == ""
	})
	pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))

	assertScrobbleCountStays(t, scrobbler, 1)
}

func TestLastfm_ChannelRoundTripDoesNotScrobbleTwice(t *testing.T) {
	shrinkLastfmThresholds(t)
	scrobbler := &fakeScrobbler{}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	time.Sleep(2 * lastfmMinPlayDuration)
	// Away to another channel: the groovesalad play is scrobbled once.
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))
	c.waitState("dronezone", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.ChannelID == "dronezone" && st.TrackTitle == ""
	})
	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")
	pushTitle(t, player, c, "Stars of the Lid - Requiem for Dying Mothers")
	time.Sleep(2 * lastfmMinPlayDuration)

	// Back again while groovesalad is still on the same track: the
	// dronezone play is scrobbled, the resumed groovesalad one is not
	// scrobbled a second time, and the next track there is a new play.
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	c.waitState("groovesalad", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.ChannelID == "groovesalad" && st.TrackTitle == ""
	})
	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 2 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, "Stars of the Lid", scrobbler.lastScrobble().artist)
	pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)
	pushTitle(t, player, c, "Tycho - A Walk")
	assertScrobbleCountStays(t, scrobbler, 2)

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))
	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 3 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, "Tycho", scrobbler.lastScrobble().artist)
}

func TestLastfm_ListenedStretchesAddUpAcrossPauses(t *testing.T) {
	prevMin := lastfmMinPlayDuration
	lastfmMinPlayDuration = 200 * time.Millisecond
	t.Cleanup(func() { lastfmMinPlayDuration = prevMin })

	scrobbler := &fakeScrobbler{}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")
	started := time.Now()

	unpause := func() {
		t.Helper()
		decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
		c.waitState("replaying", func(st protocol.PlaybackState) bool {
			return st.Status == protocol.StatusPlaying && st.TrackTitle == ""
		})
		pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	}

	// 120 ms listened, then a pause longer than the minimum: wall-clock
	// time since the first sighting now exceeds it, listened time does not.
	time.Sleep(120 * time.Millisecond)
	decodeState(t, c.call(protocol.MethodStop, nil))
	time.Sleep(250 * time.Millisecond)
	unpause()
	decodeState(t, c.call(protocol.MethodStop, nil))
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, scrobbler.scrobbleCount(), "only listened time counts towards the minimum, not time paused")

	// Another 120 ms listened takes the total past the minimum.
	unpause()
	time.Sleep(120 * time.Millisecond)
	decodeState(t, c.call(protocol.MethodStop, nil))
	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")
	got := scrobbler.lastScrobble()
	assert.WithinDuration(t, started, got.startedAt, 100*time.Millisecond, "the scrobble is stamped with the first sighting, not the last resume")
}

func TestLastfm_SameTrackBeyondWindowIsANewPlay(t *testing.T) {
	shrinkLastfmThresholds(t)
	shrinkLastfmSameTrackWindow(t, time.Nanosecond)
	scrobbler := &fakeScrobbler{}
	_, player, c := playScrobbled(t, scrobbler, "Boards of Canada - Dayvan Cowboy")

	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))
	awaitScrobble(t, scrobbler, "Boards of Canada", "Dayvan Cowboy")

	// Long after the first sighting (the window has elapsed) the station
	// really plays the track again: a distinct play, scrobbled on its own.
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	c.waitState("replaying", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.TrackTitle == ""
	})
	pushTitle(t, player, c, "Boards of Canada - Dayvan Cowboy")
	time.Sleep(2 * lastfmMinPlayDuration)
	decodeState(t, c.call(protocol.MethodStop, nil))
	require.Eventually(t, func() bool { return scrobbler.scrobbleCount() == 2 }, 2*time.Second, 5*time.Millisecond)
}
