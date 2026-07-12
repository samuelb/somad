package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setEndpoint swaps the global endpoint for a test.
func setEndpoint(t *testing.T, ep client.Endpoint) {
	t.Helper()
	prev := endpoint
	endpoint = ep
	t.Cleanup(func() { endpoint = prev })
}

// startStatusServer runs a minimal protocol server answering hello and
// status with the given snapshot.
func startStatusServer(t *testing.T, path string, st protocol.PlaybackState) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = nc.Close() }()
				sc := protocol.NewScanner(nc)
				for sc.Scan() {
					var req protocol.Request
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						continue
					}
					var result any
					switch req.Method {
					case protocol.MethodHello:
						result = protocol.HelloResult{ServerVersion: version, ProtocolVersion: protocol.Version}
					case protocol.MethodStatus:
						result = st
					default:
						continue
					}
					raw, _ := json.Marshal(result)
					_ = protocol.WriteLine(nc, protocol.Response{ID: req.ID, Result: raw})
				}
			}()
		}
	}()
}

func TestStatusSnapshot_RunningServer(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "s.sock")
	want := protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelTitle: "Groove Salad", Volume: 0.8}
	startStatusServer(t, path, want)
	setEndpoint(t, client.UnixEndpoint(path))

	assert.Equal(t, want, statusSnapshot())
}

func TestStatusSnapshot_LocalServerNotRunning(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st := &state.State{}
	st.SetVolume(0.35)
	require.NoError(t, state.SaveState(st))

	setEndpoint(t, client.UnixEndpoint(filepath.Join(t.TempDir(), "absent.sock")))

	got := statusSnapshot()
	assert.Equal(t, protocol.StatusStopped, got.Status)
	assert.Empty(t, got.StreamError, "a stopped local server is normal, not an error")
	assert.InDelta(t, 0.35, got.Volume, 1e-9, "the persisted volume completes the snapshot")
}

func TestStatusSnapshot_RemoteServerUnreachable(t *testing.T) {
	// Bind and immediately close a port so nothing answers on it.
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	setEndpoint(t, client.Endpoint{Network: "tcp", Address: addr})

	// A status bar polls this every tick: it must get parseable output with
	// the failure attached, not exit 1 with a message on stderr.
	got := statusSnapshot()
	assert.Equal(t, protocol.StatusStopped, got.Status)
	assert.NotEmpty(t, got.StreamError)
}

// shortTempDir returns a temp dir short enough for sun_path limits.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "somad")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
