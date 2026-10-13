package client

import (
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dialRaw connects to the Unix socket at path without any of the client's
// checks.
func dialRaw(t *testing.T, path string) net.Conn {
	t.Helper()
	nc, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	return nc
}

// listenRaw listens on a fresh Unix socket that accepts nothing, returning
// its path.
func listenRaw(t *testing.T) string {
	t.Helper()
	path := testSocketPath(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return path
}

func TestPeerPID_ReportsTheListeningProcess(t *testing.T) {
	nc := dialRaw(t, listenRaw(t))

	pid, err := peerPID(nc)
	if !peerPIDSupported() {
		assert.Error(t, err)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), pid)
}

func TestPeerUID_ReportsTheListeningUser(t *testing.T) {
	nc := dialRaw(t, listenRaw(t))

	uid, err := peerUID(nc)
	if !peerPIDSupported() {
		assert.ErrorIs(t, err, errPeerCredUnsupported)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, int64(os.Getuid()), int64(uid))
}

// fakePeerUID makes every Unix socket peer look like it runs as uid (or
// fail with err) for the duration of t: the fake daemons in these tests run
// in the test process, as the test's own user.
func fakePeerUID(t *testing.T, uid uint32, err error) {
	t.Helper()
	prev := peerUID
	peerUID = func(net.Conn) (uint32, error) { return uid, err }
	t.Cleanup(func() { peerUID = prev })
}

// foreignUID returns a user ID that is not the test's own.
func foreignUID() uint32 {
	if os.Getuid() == 4242 {
		return 4243
	}
	return 4242
}

// recordingHandler is defaultHandler that counts the requests it gets.
func recordingHandler(requests *atomic.Int32) func(req protocol.Request, send func(v any)) {
	return func(req protocol.Request, send func(v any)) {
		requests.Add(1)
		defaultHandler("dev")(req, send)
	}
}

func TestDialEndpoint_RefusesASocketServedByAnotherUser(t *testing.T) {
	path := testSocketPath(t)
	var requests atomic.Int32
	startFakeServer(t, path, recordingHandler(&requests))
	fakePeerUID(t, foreignUID(), nil)

	_, err := DialEndpoint(Endpoint{Network: "unix", Address: path, PSK: "sesame"})
	require.ErrorIs(t, err, ErrForeignDaemon)
	assert.Contains(t, err.Error(), path)
	assert.Zero(t, requests.Load(), "nothing, not even the PSK handshake, may reach another user's daemon")
}

func TestDialEndpoint_RefusesWhenThePeerUserIsUnknown(t *testing.T) {
	path := testSocketPath(t)
	startFakeServer(t, path, defaultHandler("dev"))
	fakePeerUID(t, 0, errors.New("getsockopt: bad file descriptor"))

	_, err := Dial(path)
	require.Error(t, err, "where peer credentials exist, failing to read them must not wave the peer through")
	assert.Contains(t, err.Error(), "bad file descriptor")
}

func TestDialEndpoint_SkipsThePeerUserCheckWhereUnsupported(t *testing.T) {
	path := testSocketPath(t)
	startFakeServer(t, path, defaultHandler("dev"))
	fakePeerUID(t, 0, errPeerCredUnsupported)

	c, err := Dial(path)
	require.NoError(t, err)
	_ = c.Close()
}

func TestDialEndpoint_TCPHasNoPeerUserCheck(t *testing.T) {
	addr := startFakeTCPServer(t, defaultHandler("dev"))
	fakePeerUID(t, foreignUID(), nil)

	c, err := DialEndpoint(Endpoint{Network: "tcp", Address: addr})
	require.NoError(t, err, "a TCP peer's user is unknowable; TLS and the PSK vouch for it")
	defer func() { _ = c.Close() }()
	_, err = c.Hello("dev")
	require.NoError(t, err)
}

func TestEnsureServer_NeverSpawnsOverAnotherUsersDaemon(t *testing.T) {
	path := testSocketPath(t)
	startFakeServer(t, path, defaultHandler("dev"))
	fakePeerUID(t, foreignUID(), nil)

	spawned := false
	prev := spawnServer
	spawnServer = func() (<-chan error, error) { spawned = true; return nil, nil }
	t.Cleanup(func() { spawnServer = prev })

	_, _, err := EnsureServer(UnixEndpoint(path), "dev")
	require.ErrorIs(t, err, ErrForeignDaemon)
	assert.False(t, spawned, "a spawn cannot take the socket over, and would hide the reason")
}
