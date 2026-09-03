package server

import (
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/platform"
	"somad/internal/protocol"
	"somad/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHello_Handshake(t *testing.T) {
	s, _ := newTestServer(t, Config{Version: "1.2.3"})
	c := connect(t, s)

	result := c.hello()

	assert.Equal(t, "1.2.3", result.ServerVersion)
	assert.Equal(t, protocol.Version, result.ProtocolVersion)
	assert.NotZero(t, result.PID)
}

func TestHello_ProtocolMismatch(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)

	resp := c.call(protocol.MethodHello, protocol.HelloParams{ProtocolVersion: 999})

	assert.Contains(t, resp.Error, "incompatible protocol version")
}

func TestHello_RequiredBeforeOtherMethods(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)

	resp := c.call(protocol.MethodStatus, nil)

	assert.Contains(t, resp.Error, "hello required")
}

func TestPlay_HappyPath(t *testing.T) {
	s, player := newTestServer(t, Config{})
	c1 := connect(t, s)
	c1.hello()
	c2 := connect(t, s)
	c2.hello()

	resp := c1.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"})
	st := decodeState(t, resp)

	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, "groovesalad", st.ChannelID)
	assert.Equal(t, "Groove Salad", st.ChannelTitle)

	// Both clients observe the connecting and playing snapshots.
	for _, c := range []*tclient{c1, c2} {
		c.waitState("connecting", func(st protocol.PlaybackState) bool {
			return st.Status == protocol.StatusConnecting && st.ChannelID == "groovesalad"
		})
		c.waitState("playing", func(st protocol.PlaybackState) bool {
			return st.Status == protocol.StatusPlaying && st.ChannelID == "groovesalad"
		})
	}

	player.mu.Lock()
	assert.Equal(t, []string{"http://somafm.com/groovesalad.pls#stream"}, player.playURLs)
	player.mu.Unlock()

	// The played channel is persisted as the last selection.
	st2, err := state.LoadState()
	require.NoError(t, err)
	assert.Equal(t, "groovesalad", st2.LastSelectedChannelID)
}

func TestPlay_SameChannelWhilePlayingIsNoOp(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}
	c.waitState("track title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})

	player.mu.Lock()
	urlsBefore := len(player.playURLs)
	player.mu.Unlock()
	genBefore := player.currentGen()

	// Re-issuing play for the channel that is already playing must be a
	// no-op: no new Play call, no reconnect, and the track title survives.
	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"})
	st := decodeState(t, resp)

	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, "groovesalad", st.ChannelID)
	assert.Equal(t, "Boards of Canada - Dayvan Cowboy", st.TrackTitle)

	player.mu.Lock()
	assert.Len(t, player.playURLs, urlsBefore, "Play must not be called again for the current channel")
	player.mu.Unlock()
	assert.Equal(t, genBefore, player.currentGen(), "no new play generation should be started")
}

func TestPlay_UnknownChannel(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "nope"})

	assert.Contains(t, resp.Error, "unknown channel")
	assert.Equal(t, protocol.StatusStopped, s.Snapshot().Status)
}

func TestPlay_NoPlayableStreamDoesNotRetry(t *testing.T) {
	s, _ := newTestServer(t, Config{}) // pinned to MP3-only; see newTestServer

	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "aacchannel"})

	assert.Contains(t, resp.Error, "no playable stream")
	snap := s.Snapshot()
	assert.Equal(t, protocol.StatusStopped, snap.Status)
	assert.Contains(t, snap.StreamError, "no playable stream")
}

func TestPlay_PrefersAACWhenSupported(t *testing.T) {
	s, player := newTestServer(t, Config{})
	supportedFormats = func() []string { return []string{audio.FormatAAC, audio.FormatMP3} }

	c := connect(t, s)
	c.hello()

	// Both formats available: the AAC playlist wins.
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"}))

	player.mu.Lock()
	assert.Equal(t, []string{"http://somafm.com/both130.pls#stream"}, player.playURLs)
	assert.Equal(t, []string{audio.FormatAAC}, player.playFormats)
	player.mu.Unlock()
}

func TestPlay_UsesConfiguredQuality(t *testing.T) {
	s, player := newTestServer(t, Config{Quality: "low"}) // pinned to MP3-only; see newTestServer

	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"}))

	player.mu.Lock()
	assert.Equal(t, []string{"http://somafm.com/both-low.pls#stream"}, player.playURLs)
	player.mu.Unlock()
}

func TestPlay_UnconfiguredQualityStillPrefersHighest(t *testing.T) {
	s, player := newTestServer(t, Config{}) // no quality preference configured

	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"}))

	player.mu.Lock()
	assert.Equal(t, []string{"http://somafm.com/both.pls#stream"}, player.playURLs)
	player.mu.Unlock()
}

func TestPlay_StopDuringFallbackDoesNotStartNextCandidate(t *testing.T) {
	s, player := newTestServer(t, Config{})
	supportedFormats = func() []string { return []string{audio.FormatAAC, audio.FormatMP3} }
	player.mu.Lock()
	// The AAC candidate fails, and while it was "connecting" a Stop arrived:
	// the MP3 fallback must not start stale audio afterwards.
	player.failFormats = map[string]bool{audio.FormatAAC: true}
	player.onPlay = func(format string) {
		if format == audio.FormatAAC {
			s.Stop()
		}
	}
	player.mu.Unlock()

	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"})

	assert.NotEmpty(t, resp.Error, "the superseded play must not report success")
	assert.Equal(t, protocol.StatusStopped, s.Snapshot().Status)
	player.mu.Lock()
	assert.Empty(t, player.playURLs, "no candidate may start after the stop")
	player.mu.Unlock()
}

func TestPlay_StopDuringSucceedingCandidateDoesNotStartAudio(t *testing.T) {
	s, player := newTestServer(t, Config{})
	// A Stop lands after the server's per-candidate guard but before the
	// player commits. The player sees the stop's newer generation and must
	// refuse the late play instead of starting audio under a stopped status.
	player.mu.Lock()
	player.onPlay = func(string) { s.Stop() }
	player.mu.Unlock()

	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"})

	assert.Contains(t, resp.Error, audio.ErrSuperseded.Error())
	assert.Equal(t, protocol.StatusStopped, s.Snapshot().Status)
	player.mu.Lock()
	assert.Empty(t, player.playURLs, "no audio may start after the stop")
	assert.False(t, player.playing)
	player.mu.Unlock()
}

func TestPlay_FallsBackToMP3WhenAACFails(t *testing.T) {
	s, player := newTestServer(t, Config{})
	supportedFormats = func() []string { return []string{audio.FormatAAC, audio.FormatMP3} }
	player.mu.Lock()
	player.failFormats = map[string]bool{audio.FormatAAC: true}
	player.mu.Unlock()

	c := connect(t, s)
	c.hello()

	st := decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"}))

	assert.Equal(t, protocol.StatusPlaying, st.Status)
	player.mu.Lock()
	assert.Equal(t, []string{"http://somafm.com/both.pls#stream"}, player.playURLs)
	assert.Equal(t, []string{audio.FormatMP3}, player.playFormats)
	player.mu.Unlock()
}

func TestStreamDrop_ReconnectsAndRecovers(t *testing.T) {
	prev := reconnectBaseDelay
	reconnectBaseDelay = time.Millisecond
	defer func() { reconnectBaseDelay = prev }()

	s, player := newTestServer(t, Config{})
	go s.watchPlayerErrors()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))

	player.errChan <- errors.New("stream read error")

	c.waitState("reconnecting", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusReconnecting && st.ReconnectAttempt == 1
	})
	c.waitState("recovered", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.ChannelID == "dronezone"
	})
}

func TestStreamDrop_KeepsRetryingUntilRecovery(t *testing.T) {
	prev := reconnectBaseDelay
	reconnectBaseDelay = time.Millisecond
	defer func() { reconnectBaseDelay = prev }()

	s, player := newTestServer(t, Config{})
	go s.watchPlayerErrors()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))

	// Every reconnect attempt fails at the player during the outage.
	player.setPlayErr(errors.New("connection refused"))
	player.errChan <- errors.New("stream read error")

	// Reconnecting must not give up: well past the old 5-attempt budget it
	// is still trying and still reports the failure.
	const outageAttempts = 8
	var seen protocol.PlaybackState
	for attempt := 1; attempt <= outageAttempts; attempt++ {
		seen = c.waitState("reconnect attempt", func(st protocol.PlaybackState) bool {
			return st.Status == protocol.StatusReconnecting && st.ReconnectAttempt == attempt
		})
	}
	assert.Contains(t, seen.StreamError, "connection refused")

	// The network comes back; the next attempt must recover playback.
	player.setPlayErr(nil)
	c.waitState("recovered", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying && st.ChannelID == "dronezone"
	})
}

func TestReconnectDelay_DoublesThenCaps(t *testing.T) {
	assert.Equal(t, 2*time.Second, reconnectDelay(1))
	assert.Equal(t, 4*time.Second, reconnectDelay(2))
	assert.Equal(t, 32*time.Second, reconnectDelay(5))
	assert.Equal(t, reconnectMaxDelay, reconnectDelay(6), "backoff must cap, not keep doubling")
	assert.Equal(t, reconnectMaxDelay, reconnectDelay(1000), "huge attempt counts must not overflow")
}

func TestChannelArtURL_PicksLargestAvailable(t *testing.T) {
	assert.Equal(t, "xl.png", channelArtURL(channels.Channel{
		Image: "small.png", LargeImage: "large.png", XLImage: "xl.png",
	}), "XLImage must win when all three are set")
	assert.Equal(t, "large.png", channelArtURL(channels.Channel{
		Image: "small.png", LargeImage: "large.png",
	}), "LargeImage must win when XLImage is unset")
	assert.Equal(t, "small.png", channelArtURL(channels.Channel{
		Image: "small.png",
	}), "Image is the fallback")
	assert.Equal(t, "", channelArtURL(channels.Channel{}), "no images set")
}

func TestStop_CancelsPendingReconnect(t *testing.T) {
	prev := reconnectBaseDelay
	reconnectBaseDelay = 50 * time.Millisecond
	defer func() { reconnectBaseDelay = prev }()

	s, player := newTestServer(t, Config{})
	go s.watchPlayerErrors()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))
	player.errChan <- errors.New("stream read error")
	c.waitState("reconnecting", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusReconnecting
	})

	st := decodeState(t, c.call(protocol.MethodStop, nil))
	assert.Equal(t, protocol.StatusStopped, st.Status)
	assert.Empty(t, st.StreamError)

	// The reconnect timer must not fire a new play attempt after stop.
	time.Sleep(3 * reconnectBaseDelay)
	snap := s.Snapshot()
	assert.Equal(t, protocol.StatusStopped, snap.Status)
	player.mu.Lock()
	assert.False(t, player.playing)
	player.mu.Unlock()
}

func TestPlay_SupersededByNewerPlay(t *testing.T) {
	s, player := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	// First play blocks inside the player until released.
	block := make(chan struct{})
	player.mu.Lock()
	player.blockPlay = block
	player.mu.Unlock()

	firstDone := make(chan protocol.PlaybackState, 1)
	go func() {
		snap, _ := s.Play("groovesalad")
		firstDone <- snap
	}()

	// Wait until the first play is connecting, then supersede it.
	c.waitState("first connecting", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusConnecting && st.ChannelID == "groovesalad"
	})
	player.mu.Lock()
	player.blockPlay = nil
	player.mu.Unlock()

	snap, err := s.Play("dronezone")
	require.NoError(t, err)
	assert.Equal(t, "dronezone", snap.ChannelID)

	close(block)
	first := <-firstDone
	// The superseded play must not have overwritten the newer state.
	assert.Equal(t, "dronezone", first.ChannelID)
	assert.Equal(t, "dronezone", s.Snapshot().ChannelID)
}

func TestPlayRelative_NextPrevAndWrap(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	// Catalog order: groovesalad, dronezone, bothformats, aacchannel (no
	// favorites).
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))

	st := decodeState(t, c.call(protocol.MethodPlayRelative, protocol.PlayRelativeParams{Delta: 1}))
	assert.Equal(t, "dronezone", st.ChannelID)

	st = decodeState(t, c.call(protocol.MethodPlayRelative, protocol.PlayRelativeParams{Delta: -1}))
	assert.Equal(t, "groovesalad", st.ChannelID)

	// Wraps around backwards to the end of the catalog; the AAC-only channel
	// there cannot play (tests pin MP3-only), which also proves the wrap
	// targeted it.
	resp := c.call(protocol.MethodPlayRelative, protocol.PlayRelativeParams{Delta: -1})
	assert.Contains(t, resp.Error, "no playable stream for AAC Only")
}

func TestPlayRelative_FromStoppedUsesLastPlayed(t *testing.T) {
	s, _ := newTestServer(t, Config{State: &state.State{LastSelectedChannelID: "dronezone"}})
	c := connect(t, s)
	c.hello()

	st := decodeState(t, c.call(protocol.MethodPlayRelative, protocol.PlayRelativeParams{Delta: -1}))

	assert.Equal(t, "groovesalad", st.ChannelID)
	assert.Equal(t, protocol.StatusPlaying, st.Status)
}

func TestPlayPause_TogglesBetweenPlayingAndStopped(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))

	st := decodeState(t, c.call(protocol.MethodPlayPause, nil))
	assert.Equal(t, protocol.StatusStopped, st.Status)

	// Unpause reconnects to the same channel.
	st = decodeState(t, c.call(protocol.MethodPlayPause, nil))
	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, "groovesalad", st.ChannelID)
}

func TestPlayPause_FromStoppedPlaysLastPlayed(t *testing.T) {
	s, _ := newTestServer(t, Config{State: &state.State{LastSelectedChannelID: "dronezone"}})
	c := connect(t, s)
	c.hello()

	st := decodeState(t, c.call(protocol.MethodPlayPause, nil))

	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, "dronezone", st.ChannelID)
}

func TestSetVolume_ClampsAndPersists(t *testing.T) {
	s, player := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	st := decodeState(t, c.call(protocol.MethodSetVolume, protocol.SetVolumeParams{Volume: 1.7}))
	assert.InDelta(t, 1.0, st.Volume, 1e-9)

	st = decodeState(t, c.call(protocol.MethodSetVolume, protocol.SetVolumeParams{Volume: 0.4}))
	assert.InDelta(t, 0.4, st.Volume, 1e-9)
	assert.InDelta(t, 0.4, player.Volume(), 1e-9)

	persisted, err := state.LoadState()
	require.NoError(t, err)
	assert.InDelta(t, 0.4, persisted.GetVolume(), 1e-9)
}

func TestToggleFavorite_PersistsAndBroadcasts(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodToggleFavorite, protocol.ToggleFavoriteParams{ChannelID: "dronezone"})
	require.Empty(t, resp.Error)
	var result protocol.FavoritesResult
	require.NoError(t, json.Unmarshal(resp.Result, &result))
	assert.Equal(t, []string{"dronezone"}, result.Favorites)

	payload := c.waitChannels("after toggle")
	assert.Equal(t, []string{"dronezone"}, payload.Favorites)
	// Catalog is re-sorted favorites-first.
	require.NotEmpty(t, payload.Channels)
	assert.Equal(t, "dronezone", payload.Channels[0].ID)

	persisted, err := state.LoadState()
	require.NoError(t, err)
	assert.Equal(t, []string{"dronezone"}, persisted.FavoriteChannelIDs)
}

func TestToggleFavorite_UnknownChannel(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodToggleFavorite, protocol.ToggleFavoriteParams{ChannelID: "nope"})

	assert.Contains(t, resp.Error, "unknown channel")
	persisted, err := state.LoadState()
	require.NoError(t, err)
	assert.Empty(t, persisted.FavoriteChannelIDs)
}

// TestToggleFavorite_ViaCommandSender covers the tray's Favorite toggle path:
// a ToggleFavoriteMsg through the MPRIS/tray command router must flip the
// flag just like the wire-protocol method. The router handles it on its own
// goroutine, so the assertions poll.
func TestToggleFavorite_ViaCommandSender(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	sender := mprisSender{s}

	sender.Send(platform.ToggleFavoriteMsg{ID: "dronezone"})
	assert.Eventually(t, func() bool {
		return slices.Equal(s.ChannelsPayload().Favorites, []string{"dronezone"})
	}, time.Second, 5*time.Millisecond, "channel was not favorited")

	sender.Send(platform.ToggleFavoriteMsg{ID: "dronezone"})
	assert.Eventually(t, func() bool {
		return len(s.ChannelsPayload().Favorites) == 0
	}, time.Second, 5*time.Millisecond, "channel was not unfavorited")
}

// TestQuit_ViaCommandSender covers the MPRIS Quit action: it must shut the
// server down like the tray's Quit item, not be a dead menu entry.
func TestQuit_ViaCommandSender(t *testing.T) {
	s, _ := newTestServer(t, Config{})

	mprisSender{s}.Send(platform.MPRISQuitMsg{})

	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("MPRIS Quit did not shut the server down")
	}
}

// TestToggleFavorite_ConcurrentReadIsRaceFree guards the favorites slice
// handed to clients: ChannelsPayload/ToggleFavorite return values are marshaled
// after the server lock is released, while ToggleFavorite mutates the backing
// slice in place under the lock. Run under -race, this fails if either path
// stops returning a copy.
func TestToggleFavorite_ConcurrentReadIsRaceFree(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	// This test hammers ToggleFavorite thousands of times to exercise the
	// slice-copy race; the disk write on each call is irrelevant here and
	// would otherwise dominate the runtime with fsyncs.
	s.persist = func(*state.State) error { return nil }

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = json.Marshal(s.ChannelsPayload())
		}
	})

	wg.Go(func() {
		defer close(stop)
		for range 2000 {
			favorites, err := s.ToggleFavorite("dronezone")
			require.NoError(t, err)
			_, _ = json.Marshal(favorites)
			_, err = s.ToggleFavorite("groovesalad")
			require.NoError(t, err)
		}
	})

	wg.Wait()
}

// TestSaveState_DropsStaleWrites verifies that a save carrying an older
// mutation sequence never clobbers the state written by a newer one, even when
// the saves arrive out of order (as concurrent callers may deliver them).
func TestSaveState_DropsStaleWrites(t *testing.T) {
	s, _ := newTestServer(t, Config{})

	var mu sync.Mutex
	var last string
	s.persist = func(st *state.State) error {
		mu.Lock()
		defer mu.Unlock()
		last = st.LastSelectedChannelID
		return nil
	}

	newer := &state.State{LastSelectedChannelID: "newer"}
	older := &state.State{LastSelectedChannelID: "older"}

	// The newer mutation (seq 2) reaches disk first; the older one (seq 1)
	// arrives late and must be dropped rather than overwrite it.
	s.saveState(2, newer)
	s.saveState(1, older)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "newer", last, "stale save clobbered newer state")
}

// TestSaveState_FailedSaveRetriedAtShutdown verifies that a persist failure
// with no further mutation does not lose the state: the failed save is kept
// dirty and flushed when the server shuts down.
func TestSaveState_FailedSaveRetriedAtShutdown(t *testing.T) {
	s, _ := newTestServer(t, Config{})

	var mu sync.Mutex
	var fail bool
	var saved []string
	s.persist = func(st *state.State) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return errors.New("disk full")
		}
		saved = append(saved, st.LastSelectedChannelID)
		return nil
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	s.saveState(1, &state.State{LastSelectedChannelID: "lost-unless-retried"})

	mu.Lock()
	fail = false
	mu.Unlock()
	s.Shutdown()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"lost-unless-retried"}, saved)
}

// TestSaveState_SuccessfulSaveClearsDirty verifies a newer successful save
// supersedes an earlier failed one, so shutdown does not resurrect old state.
func TestSaveState_SuccessfulSaveClearsDirty(t *testing.T) {
	s, _ := newTestServer(t, Config{})

	var mu sync.Mutex
	var saved []string
	s.persist = func(st *state.State) error {
		mu.Lock()
		defer mu.Unlock()
		if st.LastSelectedChannelID == "failing" {
			return errors.New("disk full")
		}
		saved = append(saved, st.LastSelectedChannelID)
		return nil
	}

	s.saveState(1, &state.State{LastSelectedChannelID: "failing"})
	s.saveState(2, &state.State{LastSelectedChannelID: "newer"})
	s.Shutdown()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"newer"}, saved, "shutdown must not replay a superseded failed save")
}

func TestTrackUpdate_BroadcastsTitle(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	player.trackChan <- audio.TrackInfo{Title: "Boards of Canada - Dayvan Cowboy", Gen: player.currentGen()}

	st := c.waitState("track title", func(st protocol.PlaybackState) bool {
		return st.TrackTitle == "Boards of Canada - Dayvan Cowboy"
	})
	assert.Equal(t, protocol.StatusPlaying, st.Status)
}

func TestTrackUpdate_StaleGenerationIsDropped(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchTrackUpdates()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))
	gen := player.currentGen()
	// The previous channel's stream is still fading out and delivers a
	// title; it must not show up under the new channel.
	player.trackChan <- audio.TrackInfo{Title: "Stale Title", Gen: gen - 1}
	player.trackChan <- audio.TrackInfo{Title: "Fresh Title", Gen: gen}

	st := c.waitState("fresh title", func(st protocol.PlaybackState) bool {
		require.NotEqual(t, "Stale Title", st.TrackTitle, "title from the old session leaked")
		return st.TrackTitle == "Fresh Title"
	})
	assert.Equal(t, "dronezone", st.ChannelID)
}

func TestStreamError_FromOldGenerationIsIgnored(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchPlayerErrors()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))
	gen := player.currentGen()

	// The old session fails during the crossfade: the new one must keep
	// playing.
	player.errChan <- &audio.StreamError{Gen: gen - 1, Err: errors.New("stream read error")}
	time.Sleep(20 * time.Millisecond)
	snap := s.Snapshot()
	assert.Equal(t, protocol.StatusPlaying, snap.Status)
	assert.Equal(t, "dronezone", snap.ChannelID)
	player.mu.Lock()
	assert.True(t, player.playing)
	player.mu.Unlock()

	// Its own generation's error does take the session down.
	player.errChan <- &audio.StreamError{Gen: gen, Err: errors.New("stream read error")}
	c.waitState("reconnecting", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusReconnecting
	})
}

func TestIdleExit_FiresWhenStoppedAndNoClients(t *testing.T) {
	s, _ := newTestServer(t, Config{IdleTimeout: 30 * time.Millisecond})

	s.mu.Lock()
	s.maybeArmIdleLocked()
	s.mu.Unlock()

	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("idle exit did not fire")
	}
}

func TestIdleExit_HeldOffByConnectedClient(t *testing.T) {
	s, _ := newTestServer(t, Config{IdleTimeout: 30 * time.Millisecond})
	c := connect(t, s)
	c.hello()

	select {
	case <-s.Done():
		t.Fatal("server exited despite a connected client")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestIdleExit_HeldOffByPlayback(t *testing.T) {
	s, _ := newTestServer(t, Config{IdleTimeout: 30 * time.Millisecond})
	c := connect(t, s)
	c.hello()
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))
	_ = c.nc.Close() // disconnect: playing keeps the server alive

	select {
	case <-s.Done():
		t.Fatal("server exited despite active playback")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestShutdownRequest_StopsServer(t *testing.T) {
	s, player := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}))

	resp := c.call(protocol.MethodShutdown, nil)
	require.Empty(t, resp.Error)

	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not complete")
	}
	player.mu.Lock()
	assert.False(t, player.playing)
	player.mu.Unlock()
}

func TestUnknownMethod(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	resp := c.call("frobnicate", nil)

	assert.Contains(t, resp.Error, "unknown method")
}
