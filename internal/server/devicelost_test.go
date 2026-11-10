package server

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/state"
)

// lostDeviceError stands in for the player's error once the audio device
// has failed for good: like the real one it is an ErrAudioDeviceLost and an
// ErrAudioDevice both.
type lostDeviceError struct{}

func (lostDeviceError) Error() string { return "audio device failed: connection closed" }

func (lostDeviceError) Is(target error) bool {
	return target == audio.ErrAudioDeviceLost || target == audio.ErrAudioDevice
}

var errDeviceLost error = lostDeviceError{}

// countResolves counts the playlist fetches from here on.
func countResolves(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	prev := resolveStreamURLs
	resolveStreamURLs = func(playlistURL, userAgent string) ([]string, error) {
		n.Add(1)
		return prev(playlistURL, userAgent)
	}
	t.Cleanup(func() { resolveStreamURLs = prev })
	return &n
}

// assertDeviceLost checks what every snapshot shows once the device is
// lost: stopped, flagged, and saying why and what helps.
func assertDeviceLost(t *testing.T, st protocol.PlaybackState) {
	t.Helper()
	assert.Equal(t, protocol.StatusStopped, st.Status)
	assert.True(t, st.DeviceLost)
	assert.Contains(t, st.StreamError, "audio device failed: connection closed")
	assert.Contains(t, st.StreamError, "has to be restarted")
}

func TestPlay_ALostAudioDeviceStopsInsteadOfReconnecting(t *testing.T) {
	s, player := newTestServer(t, Config{})
	withMirrors(t, 3)
	player.setPlayErr(errDeviceLost)

	c := connect(t, s)
	assert.False(t, c.hello().DeviceLost)
	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"})

	assert.Contains(t, resp.Error, "audio device failed")
	// No other mirror or format, and no reconnect: each would fail the
	// same way for as long as the daemon ran.
	assert.Len(t, player.attemptedURLs(), 1)
	assertDeviceLost(t, s.Snapshot())
	s.mu.Lock()
	assert.Nil(t, s.reconnectTimer)
	s.mu.Unlock()
}

// loseDevice makes a play fail for a lost audio device, which is how the
// server learns of it, and leaves the player able to play again: the device
// does not come back in this process even so.
func loseDevice(t *testing.T, s *Server, player *mockPlayer) {
	t.Helper()
	player.setPlayErr(errDeviceLost)
	_, err := s.Play("dronezone")
	require.Error(t, err)
	player.setPlayErr(nil)
}

// awaitShutdown waits for a server that is shutting itself down.
func awaitShutdown(t *testing.T, s *Server) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not shut down")
	}
}

func TestPlay_OnceTheAudioDeviceIsLostRestartsTheDaemonToPlay(t *testing.T) {
	for name, tc := range map[string]struct {
		play func(s *Server) (protocol.PlaybackState, error)
		want string
	}{
		"a channel":        {func(s *Server) (protocol.PlaybackState, error) { return s.Play("groovesalad") }, "groovesalad"},
		"the last channel": {(*Server).PlayCurrent, "dronezone"},
		"unpausing":        {(*Server).PlayPause, "dronezone"},
		"the next channel": {func(s *Server) (protocol.PlaybackState, error) { return s.PlayRelative(1) }, "bothformats"},
	} {
		t.Run(name, func(t *testing.T) {
			s, player := newTestServer(t, Config{})
			loseDevice(t, s, player)
			resolves := countResolves(t)
			attempts := len(player.attemptedURLs())

			st, err := tc.play(s)

			// This process cannot play it, so it makes way for one that
			// can, and says so instead of the old error.
			require.Error(t, err)
			assert.Contains(t, err.Error(), "restarting to play")
			assertDeviceLost(t, st)
			assert.Zero(t, resolves.Load(), "no playlist is fetched for a stream that cannot play")
			assert.Len(t, player.attemptedURLs(), attempts)
			awaitShutdown(t, s)
			resume, ok := s.Resume()
			require.True(t, ok)
			assert.Equal(t, tc.want, resume.ChannelID)
			assert.True(t, resume.StopAt.IsZero(), "no sleep timer was pending")
		})
	}
}

func TestPlay_TheRestartForALostDeviceAnswersTheRequestFirst(t *testing.T) {
	s, player := newTestServer(t, Config{})
	loseDevice(t, s, player)
	c := connect(t, s)
	assert.True(t, c.hello().DeviceLost, "a client connecting now learns it from the hello")

	// As a remote client does, which cannot replace the daemon itself.
	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"})

	assert.Contains(t, resp.Error, "restarting to play Groove Salad")
	awaitShutdown(t, s)
}

func TestPlay_TheRestartForALostDeviceCarriesTheSleepTimer(t *testing.T) {
	s, player := newTestServer(t, Config{})
	s.StopIn(time.Hour)
	loseDevice(t, s, player)

	_, err := s.Play("groovesalad")
	require.Error(t, err)

	resume, ok := s.Resume()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(time.Hour), resume.StopAt, time.Minute)
}

func TestPlay_WhatCannotPlayDoesNotRestartADaemonWithoutADevice(t *testing.T) {
	s, player := newTestServer(t, Config{})
	loseDevice(t, s, player)

	// A channel that does not exist would not play after a restart either.
	st, err := s.Play("nosuchchannel")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has to be restarted")
	assertDeviceLost(t, st)

	// Nor does anything but a request to play: a stop clears a stream's
	// error, but not the reason nothing plays.
	assertDeviceLost(t, s.Stop())
	_, ok := s.Resume()
	assert.False(t, ok)
	s.mu.Lock()
	assert.False(t, s.closing)
	s.mu.Unlock()
}

func TestResumePlayback_PlaysWhatTheDaemonBeforeWasAskedFor(t *testing.T) {
	s, player := newTestServer(t, Config{})
	stopAt := time.Now().Add(time.Hour).Truncate(time.Second)

	s.resumePlayback(state.Resume{ChannelID: "groovesalad", StopAt: stopAt})

	st := s.Snapshot()
	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, "groovesalad", st.ChannelID)
	assert.Len(t, player.attemptedURLs(), 1)
	// Its sleep timer lived in the old process only.
	assert.Equal(t, stopAt.Format(time.RFC3339), st.StopAt)
}

func TestResumePlayback_StaysSilentOnceTheSleepTimerHasRunOut(t *testing.T) {
	s, player := newTestServer(t, Config{})

	s.resumePlayback(state.Resume{ChannelID: "groovesalad", StopAt: time.Now().Add(-time.Second)})

	st := s.Snapshot()
	assert.Equal(t, protocol.StatusStopped, st.Status)
	assert.Empty(t, st.StopAt)
	assert.Empty(t, player.attemptedURLs())
}

func TestRun_ResumesTheChannelItWasStartedFor(t *testing.T) {
	isolateServer(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	require.NoError(t, channels.WriteChannelsToCache(&channels.Channels{Channels: testChannels()}))
	prevURL := channels.SomaFMChannelsURL
	channels.SomaFMChannelsURL = "http://catalog.invalid.test/channels.json"
	t.Cleanup(func() { channels.SomaFMChannelsURL = prevURL })
	player := newMockPlayer()
	s := New(Config{Player: player, State: &state.State{}, Version: "test", Resume: state.Resume{ChannelID: "dronezone"}})
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ln) }()
	t.Cleanup(func() {
		s.Shutdown()
		assert.NoError(t, <-runDone)
	})

	require.Eventually(t, func() bool {
		st := s.Snapshot()
		return st.Status == protocol.StatusPlaying && st.ChannelID == "dronezone"
	}, 5*time.Second, 5*time.Millisecond)
}

func TestStreamError_ALostAudioDeviceStopsPlayback(t *testing.T) {
	s, player := newTestServer(t, Config{})
	go s.watchPlayerErrors()
	c := connect(t, s)
	c.hello()
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))

	// The session playing notices the device going away and reports it.
	player.errChan <- &audio.StreamError{Gen: player.currentGen(), Err: errDeviceLost}

	st := c.waitState("stopped for the lost device", func(st protocol.PlaybackState) bool {
		return st.DeviceLost
	})
	assertDeviceLost(t, st)
	s.mu.Lock()
	assert.Nil(t, s.reconnectTimer, "a reconnect could never play")
	s.mu.Unlock()
}

func TestStreamError_ALostAudioDeviceEndsAPendingReconnect(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	drop, _ := manualReconnects(t, s, "dronezone")
	_, err := s.Play("dronezone")
	require.NoError(t, err)
	drop()

	// Reported by a session other than the current one: the device is
	// gone for all of them.
	s.handleStreamError(&audio.StreamError{Gen: 0, Err: errDeviceLost})

	assertDeviceLost(t, s.Snapshot())
	s.mu.Lock()
	assert.Nil(t, s.reconnectTimer)
	s.mu.Unlock()
}

func TestPlay_ADeviceNotReadyInTimeIsStillRetried(t *testing.T) {
	prev := reconnectBaseDelay
	reconnectBaseDelay = time.Hour
	defer func() { reconnectBaseDelay = prev }()

	s, player := newTestServer(t, Config{})
	player.setPlayErr(fmt.Errorf("audio device not ready after 15s: %w", audio.ErrAudioDevice))

	_, err := s.Play("dronezone")
	require.Error(t, err)

	st := s.Snapshot()
	assert.Equal(t, protocol.StatusReconnecting, st.Status)
	assert.False(t, st.DeviceLost, "a later play can still find the device ready")
}
