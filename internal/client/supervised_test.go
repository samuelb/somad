package client

import (
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here cover the restart of a daemon that a service manager runs
// (soma daemon --supervised, ADR-0038). Spawning a replacement, as for any
// other daemon, took such a daemon out of its service manager's hands: the
// spawned one held the socket and the lock, and the unit's own instance, if
// it was started again at all, found a daemon running and exited.

// supervisedServer is a fake daemon that reports whether it is supervised
// and records the shutdowns it is asked for. Like a real one it stops
// listening when shut down; onRestart then stands in for the service
// manager.
type supervisedServer struct {
	supervised bool
	// onRestart, when set, runs after a shutdown that asked for a restart.
	onRestart func()

	mu        sync.Mutex
	shutdowns []protocol.ShutdownParams
}

func (ss *supervisedServer) start(t *testing.T, path string) {
	t.Helper()
	var ln net.Listener
	fs := startFakeServer(t, path, func(req protocol.Request, send func(v any)) {
		respond := func(result any) {
			raw, _ := json.Marshal(result)
			send(protocol.Response{ID: req.ID, Result: raw})
		}
		switch req.Method {
		case protocol.MethodHello:
			respond(protocol.HelloResult{ServerVersion: "v1", ProtocolVersion: protocol.Version, Supervised: ss.supervised})
		case protocol.MethodStatus:
			respond(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})
		case protocol.MethodShutdown:
			var p protocol.ShutdownParams
			_ = json.Unmarshal(req.Params, &p)
			ss.mu.Lock()
			ss.shutdowns = append(ss.shutdowns, p)
			ss.mu.Unlock()
			respond(struct{}{})
			_ = ln.Close()
			if p.Restart && ss.onRestart != nil {
				go ss.onRestart()
			}
		}
	})
	ln = fs.ln
}

func (ss *supervisedServer) shutdownRequests() []protocol.ShutdownParams {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return append([]protocol.ShutdownParams(nil), ss.shutdowns...)
}

// countSpawns makes a spawn bring up next on path and counts them.
func countSpawns(t *testing.T, path string, next *supervisedServer) *atomic.Int32 {
	t.Helper()
	var spawns atomic.Int32
	prev := spawnServer
	spawnServer = func() (<-chan error, error) {
		spawns.Add(1)
		next.start(t, path)
		return nil, nil
	}
	t.Cleanup(func() { spawnServer = prev })
	return &spawns
}

func TestRestart_WaitsForASupervisedDaemonsServiceManager(t *testing.T) {
	path := testSocketPath(t)
	fresh := &supervisedServer{supervised: true}
	old := &supervisedServer{supervised: true}
	old.onRestart = func() {
		// The service manager takes a moment to start it again.
		time.Sleep(150 * time.Millisecond)
		fresh.start(t, path)
	}
	old.start(t, path)
	spawns := countSpawns(t, path, &supervisedServer{})

	c, _, err := connectOrSpawn(UnixEndpoint(path), "v1")
	require.NoError(t, err)
	nc, hr, err := Restart(c, UnixEndpoint(path), "v1")
	require.NoError(t, err)
	defer func() { _ = nc.Close() }()

	assert.Equal(t, []protocol.ShutdownParams{{Restart: true}}, old.shutdownRequests())
	assert.Zero(t, spawns.Load(), "a daemon spawned from here would take the socket from the service manager's")
	assert.True(t, hr.Supervised, "the caller talks to the instance the service manager started")
}

func TestRestart_SpawnsWhenASupervisedDaemonIsNotStartedAgain(t *testing.T) {
	prev := supervisedRestartWait
	supervisedRestartWait = 200 * time.Millisecond
	defer func() { supervisedRestartWait = prev }()

	path := testSocketPath(t)
	old := &supervisedServer{supervised: true} // and no service manager behind it
	old.start(t, path)
	spawns := countSpawns(t, path, &supervisedServer{})

	c, _, err := connectOrSpawn(UnixEndpoint(path), "v1")
	require.NoError(t, err)
	nc, hr, err := Restart(c, UnixEndpoint(path), "v1")
	require.NoError(t, err)
	defer func() { _ = nc.Close() }()

	// The user's command outranks keeping the daemon supervised.
	assert.EqualValues(t, 1, spawns.Load())
	assert.False(t, hr.Supervised)
}

func TestRestart_SpawnsAtOnceForADaemonOnItsOwn(t *testing.T) {
	path := testSocketPath(t)
	old := &supervisedServer{}
	old.start(t, path)
	spawns := countSpawns(t, path, &supervisedServer{})

	c, _, err := connectOrSpawn(UnixEndpoint(path), "v1")
	require.NoError(t, err)
	started := time.Now()
	nc, _, err := Restart(c, UnixEndpoint(path), "v1")
	require.NoError(t, err)
	defer func() { _ = nc.Close() }()

	assert.EqualValues(t, 1, spawns.Load())
	assert.Less(t, time.Since(started), supervisedRestartWait, "nothing to wait for")
	// It asks for a restart all the same: only the daemon knows whether a
	// service manager runs it, and one on its own shuts down as ever.
	assert.Equal(t, []protocol.ShutdownParams{{Restart: true}}, old.shutdownRequests())
}
