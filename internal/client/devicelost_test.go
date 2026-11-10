package client

import (
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here cover a daemon whose audio device has failed for good
// (ADR-0037): it says so in its hello, plays nothing until it is replaced,
// and a local client replaces it before a command that asks for playback.

// startDeviceServer runs a fake daemon of the client's own version that
// reports its audio device as lost or not, and counts the shutdowns it is
// asked for. Like a real one, it stops listening when asked to shut down.
func startDeviceServer(t *testing.T, path string, lost bool) *atomic.Int32 {
	t.Helper()
	var shutdowns atomic.Int32
	var ln net.Listener
	fs := startFakeServer(t, path, func(req protocol.Request, send func(v any)) {
		respond := func(result any) {
			raw, _ := json.Marshal(result)
			send(protocol.Response{ID: req.ID, Result: raw})
		}
		switch req.Method {
		case protocol.MethodHello:
			respond(protocol.HelloResult{ServerVersion: "v1", ProtocolVersion: protocol.Version, DeviceLost: lost})
		case protocol.MethodStatus:
			respond(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1, DeviceLost: lost})
		case protocol.MethodShutdown:
			shutdowns.Add(1)
			respond(struct{}{})
			_ = ln.Close()
		}
	})
	ln = fs.ln
	return &shutdowns
}

// spawnsDeviceServer makes a spawn bring up a daemon with a working device
// on path, and returns how often that happened.
func spawnsDeviceServer(t *testing.T, path string) *atomic.Int32 {
	t.Helper()
	var spawns atomic.Int32
	prev := spawnServer
	spawnServer = func() (<-chan error, error) {
		spawns.Add(1)
		startDeviceServer(t, path, false)
		return nil, nil
	}
	t.Cleanup(func() { spawnServer = prev })
	return &spawns
}

func TestEnsureServerForPlayback_ReplacesADaemonWhoseAudioDeviceIsLost(t *testing.T) {
	path := testSocketPath(t)
	shutdowns := startDeviceServer(t, path, true)
	spawns := spawnsDeviceServer(t, path)

	c, hr, err := EnsureServerForPlayback(UnixEndpoint(path), "v1")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// The same version, so nothing but the device calls for it: only a new
	// process gets one that works.
	assert.EqualValues(t, 1, shutdowns.Load())
	assert.EqualValues(t, 1, spawns.Load())
	assert.False(t, hr.DeviceLost, "the caller gets the daemon that can play")
	assert.False(t, DeviceLost(c, UnixEndpoint(path)))
}

func TestEnsureServer_LeavesADaemonWhoseAudioDeviceIsLost(t *testing.T) {
	path := testSocketPath(t)
	shutdowns := startDeviceServer(t, path, true)
	spawns := spawnsDeviceServer(t, path)

	c, hr, err := EnsureServer(UnixEndpoint(path), "v1")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// A command that only looks must find the reason still there to see.
	assert.Zero(t, shutdowns.Load())
	assert.Zero(t, spawns.Load())
	assert.True(t, hr.DeviceLost)
	assert.True(t, DeviceLost(c, UnixEndpoint(path)))
}

func TestDeviceLost_NeverForARemoteDaemon(t *testing.T) {
	c := &Client{hello: protocol.HelloResult{DeviceLost: true}}

	// Nothing here could start it again (ADR-0006).
	assert.False(t, DeviceLost(c, Endpoint{Network: "tcp", Address: "radio.example:5454"}))
	assert.True(t, DeviceLost(c, UnixEndpoint("/run/somad.sock")))
}
