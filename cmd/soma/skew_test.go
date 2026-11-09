package main

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"somad/internal/channels"
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
