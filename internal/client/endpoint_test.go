package client

import (
	"context"
	"net"
	"testing"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startFakeTCPServer is startFakeServer on a loopback TCP port, returning
// the address to dial.
func startFakeTCPServer(t *testing.T, handle func(req protocol.Request, send func(v any))) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fs := &fakeServer{t: t, ln: ln, handle: handle}
	go fs.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// unreachableTCPAddr returns a loopback address nothing is listening on.
func unreachableTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func TestEndpoint_IsLocalAndString(t *testing.T) {
	local := UnixEndpoint("/run/somad.sock")
	assert.True(t, local.IsLocal())
	assert.Equal(t, "/run/somad.sock", local.String())

	remote := Endpoint{Network: "tcp", Address: "myserver:5454"}
	assert.False(t, remote.IsLocal())
	assert.Equal(t, "tcp://myserver:5454", remote.String())
}

func TestEnsureServer_RemoteUnreachableNeverSpawns(t *testing.T) {
	spawned := false
	prev := spawnServer
	spawnServer = func() error { spawned = true; return nil }
	t.Cleanup(func() { spawnServer = prev })

	ep := Endpoint{Network: "tcp", Address: unreachableTCPAddr(t)}
	_, _, err := EnsureServer(ep, "dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not started automatically")
	assert.False(t, spawned, "an unreachable remote endpoint must not spawn a local server")
}

func TestEnsureServerForPlayback_RemoteSkewedServerIsLeftAlone(t *testing.T) {
	shutdownRequested := false
	addr := startFakeTCPServer(t, func(req protocol.Request, send func(v any)) {
		if req.Method == protocol.MethodShutdown {
			shutdownRequested = true
		}
		defaultHandler("old")(req, send)
	})

	ep := Endpoint{Network: "tcp", Address: addr}
	c, hr, err := EnsureServerForPlayback(ep, "new")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	assert.Equal(t, "old", hr.ServerVersion, "the remote server keeps running its own version")
	assert.False(t, shutdownRequested, "a remote server must never be restarted for an upgrade")

	// The skewed-but-compatible connection is fully usable.
	st, err := c.Status()
	require.NoError(t, err)
	assert.Equal(t, protocol.StatusStopped, st.Status)
}

func TestRestart_RemoteEndpointRefuses(t *testing.T) {
	addr := startFakeTCPServer(t, defaultHandler("old"))
	ep := Endpoint{Network: "tcp", Address: addr}
	c, err := DialEndpoint(ep)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	_, _, err = Restart(c, ep, "new")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote")
}
