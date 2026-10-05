package server

import (
	"encoding/json"
	"testing"
	"time"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastSpectrum shrinks the spectrum frame interval for the test.
func fastSpectrum(t *testing.T) {
	t.Helper()
	prev := spectrumInterval.Load()
	spectrumInterval.Store(int64(2 * time.Millisecond))
	t.Cleanup(func() { spectrumInterval.Store(prev) })
}

// nextSpectrum returns the levels of the next spectrum event within wait,
// skipping other events, or false when none arrives.
func (c *tclient) nextSpectrum(wait time.Duration) ([]byte, bool) {
	c.t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				return nil, false
			}
			if ev.Event != protocol.EventSpectrum {
				continue
			}
			var frame protocol.SpectrumEvent
			require.NoError(c.t, json.Unmarshal(ev.Data, &frame))
			return frame.Levels, true
		case <-deadline:
			return nil, false
		}
	}
}

// drainSpectrum reads spectrum events until none arrives for quiet, and
// returns the last one; it fails the test if they never stop.
func (c *tclient) drainSpectrum(quiet time.Duration) []byte {
	c.t.Helper()
	var last []byte
	for range 1000 {
		levels, ok := c.nextSpectrum(quiet)
		if !ok {
			return last
		}
		last = levels
	}
	c.t.Fatal("spectrum events never stopped")
	return nil
}

func TestSpectrum_FramesWhilePlayingThenSettle(t *testing.T) {
	fastSpectrum(t)
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	require.Empty(t, c.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 12}).Error)
	_, ok := c.nextSpectrum(50 * time.Millisecond)
	assert.False(t, ok, "nothing has played: no frames")

	require.Empty(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}).Error)
	levels, ok := c.nextSpectrum(time.Second)
	require.True(t, ok, "frames while playing")
	assert.Len(t, levels, 12)

	require.Empty(t, c.call(protocol.MethodStop, nil).Error)
	// The silent tail lets the bars settle, then the frames pause.
	assert.Equal(t, make([]byte, 12), c.drainSpectrum(100*time.Millisecond))

	require.Empty(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}).Error)
	_, ok = c.nextSpectrum(time.Second)
	assert.True(t, ok, "frames resume with playback")
}

func TestSpectrum_OnlySubscribersGetFramesAndUnsubscribeStopsThem(t *testing.T) {
	fastSpectrum(t)
	s, _ := newTestServer(t, Config{})
	sub, other := connect(t, s), connect(t, s)
	sub.hello()
	other.hello()

	require.Empty(t, sub.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "groovesalad"}).Error)
	require.Empty(t, sub.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 5}).Error)
	levels, ok := sub.nextSpectrum(time.Second)
	require.True(t, ok)
	assert.Len(t, levels, 5)

	// A new band count replaces the old one.
	require.Empty(t, sub.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 9}).Error)
	for range 100 {
		levels, ok = sub.nextSpectrum(time.Second)
		require.True(t, ok)
		if len(levels) == 9 {
			break
		}
	}
	assert.Len(t, levels, 9)

	_, ok = other.nextSpectrum(50 * time.Millisecond)
	assert.False(t, ok, "a connection that did not subscribe gets no frames")

	require.Empty(t, sub.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 0}).Error)
	sub.drainSpectrum(100 * time.Millisecond)
	assert.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.spectrumRunning
	}, time.Second, 5*time.Millisecond, "the frame loop ends with its last subscriber")
}

func TestSpectrum_LoopEndsWhenTheSubscriberDisconnects(t *testing.T) {
	fastSpectrum(t)
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()
	require.Empty(t, c.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 3}).Error)
	_ = c.nc.Close()
	assert.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return !s.spectrumRunning
	}, time.Second, 5*time.Millisecond)
}

func TestSpectrum_RejectsBandCountsOutOfRange(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()
	assert.Contains(t, c.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: -1}).Error, "between 0 and 512")
	assert.Contains(t, c.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 513}).Error, "between 0 and 512")
	assert.Empty(t, c.call(protocol.MethodSpectrum, protocol.SpectrumParams{Bands: 512}).Error)
}
