package client

import (
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"testing"

	"somad/internal/protocol"
	"somad/internal/tlsutil"

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
	spawnServer = func() (<-chan error, error) { spawned = true; return nil, nil }
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

func TestDialEndpoint_HostnameMismatchNamesTheCertificateHosts(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_, err := tlsutil.EnsureServerCert(certPath, keyPath, nil)
	require.NoError(t, err)
	serverCfg, _, err := tlsutil.ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fs := &fakeServer{t: t, ln: tls.NewListener(ln, serverCfg), handle: defaultHandler("dev")}
	go fs.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })

	// --tls-ca with the daemon's own certificate, dialed by a LAN address
	// the auto-generated certificate does not name.
	clientCfg, err := tlsutil.ClientTLSConfig(certPath, "", "192.168.1.20")
	require.NoError(t, err)
	_, err = DialEndpoint(Endpoint{Network: "tcp", Address: ln.Addr().String(), TLS: clientCfg})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS handshake")
	assert.Contains(t, err.Error(), "certificate names localhost")
	assert.Contains(t, err.Error(), "--tls-fingerprint")
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
