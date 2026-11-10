package main

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"somad/internal/channels"
	"somad/internal/client"
	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSkewedDaemon runs a fakeDaemon that reports another soma version than
// the one runSkewedSoma's client claims, in the given playback status.
func startSkewedDaemon(t *testing.T, status string) *fakeDaemon {
	t.Helper()
	d := startFakeDaemon(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.serverVersion = "1.0.0"
	if status != protocol.StatusStopped {
		d.setPlayingLocked("dronezone")
		d.status.Status = status
	}
	return d
}

// runSkewedSoma runs soma (see runSoma) as a newer version than
// startSkewedDaemon's, against the daemon on the current endpoint.
func runSkewedSoma(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runSoma(t, shortTempDir(t), []string{mainVersionEnv + "=2.0.0", "SOMAD_SOCKET=" + endpoint.Address}, args...)
}

// asNewerSoma makes the commands called in this test process a newer soma
// than startSkewedDaemon's daemon, for the rest of the test: the test binary
// is "dev" otherwise, which never counts as skewed.
func asNewerSoma(t *testing.T) {
	t.Helper()
	prev := version
	version = "2.0.0"
	t.Cleanup(func() { version = prev })
}

// replacement is the daemon a restart brings up in a test; see
// spawnsReplacement.
type replacement struct {
	spawned []*fakeDaemon
}

// daemon returns the one daemon that must have been spawned.
func (r *replacement) daemon(t *testing.T) *fakeDaemon {
	t.Helper()
	require.Len(t, r.spawned, 1, "the daemon must have been restarted, once")
	return r.spawned[0]
}

// spawnsReplacement fakes the one step of a restart that cannot run in a
// test, the launch of the new daemon's process (client.SetSpawnServer): a
// fresh fakeDaemon of the client's own version takes over the socket
// instead. Shutting the old one down, waiting for it to let go, connecting
// and greeting are all the real thing.
func spawnsReplacement(t *testing.T) *replacement {
	t.Helper()
	r := &replacement{}
	path := endpoint.Address
	t.Cleanup(client.SetSpawnServer(func() (<-chan error, error) {
		r.spawned = append(r.spawned, startFakeDaemonAt(t, path))
		return nil, nil
	}))
	return r
}

// stopInOf parses the one sleep timer d must have been armed with.
func stopInOf(t *testing.T, d *fakeDaemon) time.Duration {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	require.Len(t, d.stopIns, 1, "the daemon must have been armed with a sleep timer, once")
	in, err := time.ParseDuration(d.stopIns[0])
	require.NoError(t, err)
	return in
}

// TestRunPlay_CarriesTheSleepTimerAcrossTheRestart covers `soma stop --in
// 45m` followed, after an upgrade, by a channel change: the restart onto
// the new version must not leave the music playing all night.
func TestRunPlay_CarriesTheSleepTimerAcrossTheRestart(t *testing.T) {
	asNewerSoma(t)
	old := startSkewedDaemon(t, protocol.StatusPlaying)
	old.mu.Lock()
	old.status.StopAt = time.Now().Add(45 * time.Minute).Format(time.RFC3339)
	old.mu.Unlock()
	restart := spawnsReplacement(t)

	out := captureStdout(t, func() { runPlay([]string{"groove"}) })

	assert.Contains(t, out, "Playing: Groove Salad")
	fresh := restart.daemon(t)
	in := stopInOf(t, fresh)
	assert.LessOrEqual(t, in, 45*time.Minute)
	assert.Greater(t, in, 44*time.Minute, "what was left of the timer")
	fresh.mu.Lock()
	defer fresh.mu.Unlock()
	assert.Equal(t, []string{"groovesalad"}, fresh.plays)
	assert.NotEmpty(t, fresh.status.StopAt, "the new daemon's snapshot must show the timer")
}

// TestRunPause_DropsTheSleepTimerTheRestartCarried covers the other side: a
// pause is a stop to the daemon, and a stop cancels the sleep timer. The
// restart a pause triggers must not leave the carried timer armed on the
// stopped daemon, to cut off whatever the user plays next.
func TestRunPause_DropsTheSleepTimerTheRestartCarried(t *testing.T) {
	asNewerSoma(t)
	old := startSkewedDaemon(t, protocol.StatusPlaying)
	old.mu.Lock()
	old.status.StopAt = time.Now().Add(45 * time.Minute).Format(time.RFC3339)
	old.mu.Unlock()
	restart := spawnsReplacement(t)

	out := captureStdout(t, func() { runPause(nil) })

	assert.Contains(t, out, "Paused")
	fresh := restart.daemon(t)
	fresh.mu.Lock()
	defer fresh.mu.Unlock()
	assert.Equal(t, 1, fresh.stops, "the pause must reach the new daemon as a stop")
	assert.Equal(t, protocol.StatusStopped, fresh.status.Status)
	assert.Empty(t, fresh.status.StopAt, "no sleep timer may stay armed after a pause")
	assert.Empty(t, fresh.plays)
}

// TestRunPlay_BadChannelLeavesASkewedDaemonPlaying covers soma play after an
// upgrade, while the old daemon plays: a channel argument that turns out to
// be no good must not restart the daemon first, which would stop the music
// for nothing.
func TestRunPlay_BadChannelLeavesASkewedDaemonPlaying(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		lastChannel string
		wantOutput  string
	}{
		{name: "no match", args: []string{"play", "typo"}, lastChannel: "groovesalad", wantOutput: `no channel matches "typo"`},
		// Both channels' IDs contain an "o".
		{name: "ambiguous", args: []string{"play", "o"}, lastChannel: "groovesalad", wantOutput: `"o" is ambiguous`},
		{name: "nothing to resume", args: []string{"play"}, wantOutput: "no previously played channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := startSkewedDaemon(t, protocol.StatusPlaying)
			d.mu.Lock()
			d.payload.LastChannelID = tt.lastChannel
			d.mu.Unlock()

			out, status := runSkewedSoma(t, tt.args...)

			assert.Equal(t, 1, status, "output: %s", out)
			assert.Contains(t, out, tt.wantOutput)
			d.mu.Lock()
			defer d.mu.Unlock()
			assert.Equal(t, []string{protocol.MethodHello, protocol.MethodChannels}, d.methods,
				"the daemon must be asked for its catalog and nothing else")
			assert.Zero(t, d.shutdowns, "a command that cannot go ahead must not restart the daemon")
			assert.Equal(t, protocol.StatusPlaying, d.status.Status)
		})
	}
}

// TestRunPlay_BadChannelLeavesAProtocolSkewedDaemonRunning is the same for a
// daemon that speaks another protocol version: it cannot be asked for its
// catalog, so the cached one stands in before the daemon is signalled.
func TestRunPlay_BadChannelLeavesAProtocolSkewedDaemonRunning(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("reading a socket's peer PID is only implemented on macOS and Linux")
	}
	dir := shortTempDir(t)
	path := filepath.Join(dir, "s.sock")
	terminated := startIncompatibleDaemon(t, path)
	t.Setenv("XDG_CACHE_HOME", dir) // runSoma's cache directory
	require.NoError(t, channels.WriteChannelsToCache(&channels.Channels{Channels: testCatalog}))

	out, status := runSoma(t, dir, nil, "play", "typo")

	assert.Equal(t, 1, status, "output: %s", out)
	assert.Contains(t, out, `no channel matches "typo"`)
	select {
	case <-terminated:
		t.Fatal("a command that cannot go ahead must not stop the daemon")
	case <-time.After(300 * time.Millisecond):
	}
}
