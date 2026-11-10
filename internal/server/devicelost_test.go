package server

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"somad/internal/audio"
	"somad/internal/protocol"
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

func TestPlay_IsRefusedAtOnceOnceTheAudioDeviceIsLost(t *testing.T) {
	s, player := newTestServer(t, Config{})
	player.setPlayErr(errDeviceLost)
	_, err := s.Play("dronezone")
	require.Error(t, err)

	// Even a player that would play again is not asked: the device does
	// not come back in this process.
	player.setPlayErr(nil)
	resolves := countResolves(t)
	attempts := len(player.attemptedURLs())

	for _, play := range []func() (protocol.PlaybackState, error){
		func() (protocol.PlaybackState, error) { return s.Play("groovesalad") },
		s.PlayCurrent,
		s.PlayPause,
		func() (protocol.PlaybackState, error) { return s.PlayRelative(1) },
	} {
		st, err := play()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "has to be restarted")
		assertDeviceLost(t, st)
	}
	assert.Zero(t, resolves.Load(), "no playlist is fetched for a stream that cannot play")
	assert.Len(t, player.attemptedURLs(), attempts)

	// A stop clears a stream's error, but not the reason nothing plays.
	assertDeviceLost(t, s.Stop())

	// A client connecting now learns it from the hello.
	c := connect(t, s)
	assert.True(t, c.hello().DeviceLost)
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
