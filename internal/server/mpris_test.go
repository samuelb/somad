package server

import (
	"testing"
	"time"

	"somad/internal/platform"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMPRISVolume_SendDoesNotBlockOnServerLock guards against the MPRIS
// volume deadlock: godbus delivers a Volume write while holding its property
// lock, and the server takes that same lock (mirroring state to MPRIS)
// while holding s.mu. Send must therefore return without waiting for s.mu,
// and the queued writes must still land afterwards, newest last.
func TestMPRISVolume_SendDoesNotBlockOnServerLock(t *testing.T) {
	s, player := newTestServer(t, Config{})
	sender := mprisSender{s}

	s.mu.Lock()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		for _, v := range []float64{0.1, 0.2, 0.3} {
			sender.Send(platform.MPRISVolumeMsg{Volume: v})
		}
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("MPRIS volume Send blocked on the server lock")
	}
	s.mu.Unlock()

	require.Eventually(t, func() bool { return player.Volume() == 0.3 },
		time.Second, 5*time.Millisecond, "the newest MPRIS volume must be applied")
	assert.InDelta(t, 0.3, s.Snapshot().Volume, 1e-9)
}

// TestMPRISVolume_WorkerStopsOnShutdown checks that a volume write arriving
// after Shutdown is neither applied nor blocks the caller.
func TestMPRISVolume_WorkerStopsOnShutdown(t *testing.T) {
	s, player := newTestServer(t, Config{})
	s.Shutdown()

	sender := mprisSender{s}
	sender.Send(platform.MPRISVolumeMsg{Volume: 0.2})
	sender.Send(platform.MPRISVolumeMsg{Volume: 0.4})

	time.Sleep(50 * time.Millisecond)
	assert.InDelta(t, 1.0, player.Volume(), 1e-9, "no volume change may be applied after shutdown")
}
