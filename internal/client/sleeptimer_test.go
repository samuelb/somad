package client

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here cover a sleep timer (`soma stop --in`) pending when an
// out-of-date daemon is restarted. It lives in the old daemon's memory
// only, so the restart used to lose it without a word and the music played
// on past the time the user had set.

// timerServer is a fake daemon for the sleep timer's trip across a restart.
type timerServer struct {
	version string
	// stopAt is the sleep timer the daemon reports in its status, as the
	// wire carries it; empty for none.
	stopAt string
	// statusErr and stopErr, when set, fail those requests.
	statusErr, stopErr string

	mu    sync.Mutex
	stops []protocol.StopParams // every stop request received
}

// start serves the daemon on path. Like a real one, it stops listening when
// asked to shut down.
func (ts *timerServer) start(t *testing.T, path string) {
	t.Helper()
	var ln net.Listener
	fs := startFakeServer(t, path, func(req protocol.Request, send func(v any)) {
		respond := func(result any) {
			raw, _ := json.Marshal(result)
			send(protocol.Response{ID: req.ID, Result: raw})
		}
		switch req.Method {
		case protocol.MethodHello:
			respond(protocol.HelloResult{ServerVersion: ts.version, ProtocolVersion: protocol.Version})
		case protocol.MethodStatus:
			if ts.statusErr != "" {
				send(protocol.Response{ID: req.ID, Error: ts.statusErr})
				return
			}
			respond(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1, StopAt: ts.stopAt})
		case protocol.MethodStop:
			var p protocol.StopParams
			_ = json.Unmarshal(req.Params, &p)
			ts.mu.Lock()
			ts.stops = append(ts.stops, p)
			ts.mu.Unlock()
			if ts.stopErr != "" {
				send(protocol.Response{ID: req.ID, Error: ts.stopErr})
				return
			}
			respond(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})
		case protocol.MethodShutdown:
			respond(struct{}{})
			_ = ln.Close()
		}
	})
	ln = fs.ln
}

func (ts *timerServer) stopRequests() []protocol.StopParams {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]protocol.StopParams(nil), ts.stops...)
}

// restartOnto starts old on a socket of its own and restarts it with
// Restart, whose spawn brings up fresh in its place. It returns what
// OnSleepTimerLost was told meanwhile.
func restartOnto(t *testing.T, old, fresh *timerServer) (lost []error) {
	t.Helper()
	path := testSocketPath(t)
	old.start(t, path)

	prevSpawn := spawnServer
	spawnServer = func() (<-chan error, error) {
		fresh.start(t, path)
		return nil, nil
	}
	t.Cleanup(func() { spawnServer = prevSpawn })
	prevLost := OnSleepTimerLost
	OnSleepTimerLost = func(err error) { lost = append(lost, err) }
	t.Cleanup(func() { OnSleepTimerLost = prevLost })

	ep := UnixEndpoint(path)
	c, err := Dial(path)
	require.NoError(t, err)
	_, err = c.Hello("new")
	require.NoError(t, err)

	nc, hr, err := Restart(c, ep, "new")

	require.NoError(t, err, "a timer that cannot be carried must not fail the restart")
	t.Cleanup(func() { _ = nc.Close() })
	assert.Equal(t, fresh.version, hr.ServerVersion)
	_, err = nc.Status()
	require.NoError(t, err, "the returned client must be usable")
	return lost
}

func wireTime(at time.Time) string { return at.Format(time.RFC3339) }

func TestRestart_CarriesThePendingSleepTimer(t *testing.T) {
	old := &timerServer{version: "old", stopAt: wireTime(time.Now().Add(45 * time.Minute))}
	fresh := &timerServer{version: "new"}

	lost := restartOnto(t, old, fresh)

	assert.Empty(t, lost)
	assert.Empty(t, old.stopRequests(), "the old daemon is only asked, and shut down")
	stops := fresh.stopRequests()
	require.Len(t, stops, 1, "the new daemon must be armed with the timer")
	assert.False(t, stops[0].Cancel)
	remaining, err := time.ParseDuration(stops[0].In)
	require.NoError(t, err)
	// What is left of the 45 minutes: the wire's whole seconds and the
	// restart itself take a little off.
	assert.LessOrEqual(t, remaining, 45*time.Minute)
	assert.Greater(t, remaining, 44*time.Minute)
}

func TestRestart_ArmsNoTimerWhereNoneWasPending(t *testing.T) {
	for name, stopAt := range map[string]string{
		"none pending": "",
		// The old daemon's stop and the restart come to the same thing, and
		// what plays after it was started knowingly.
		"already due": wireTime(time.Now().Add(-2 * time.Second)),
	} {
		t.Run(name, func(t *testing.T) {
			fresh := &timerServer{version: "new"}

			lost := restartOnto(t, &timerServer{version: "old", stopAt: stopAt}, fresh)

			assert.Empty(t, lost)
			assert.Empty(t, fresh.stopRequests())
		})
	}
}

func TestRestart_ReportsASleepTimerItCouldNotCarry(t *testing.T) {
	pending := wireTime(time.Now().Add(45 * time.Minute))

	t.Run("the new daemon refuses it", func(t *testing.T) {
		fresh := &timerServer{version: "new", stopErr: "no timers today"}

		lost := restartOnto(t, &timerServer{version: "old", stopAt: pending}, fresh)

		require.Len(t, lost, 1)
		assert.Contains(t, lost[0].Error(), "no timers today")
		assert.Contains(t, lost[0].Error(), "did not take it over")
	})
	t.Run("the old daemon cannot be asked", func(t *testing.T) {
		fresh := &timerServer{version: "new"}

		lost := restartOnto(t, &timerServer{version: "old", statusErr: "busy"}, fresh)

		// Whether one was pending is unknown, which the user has to hear too.
		require.Len(t, lost, 1)
		assert.Contains(t, lost[0].Error(), "could not be asked")
		assert.Empty(t, fresh.stopRequests())
	})
	t.Run("the old daemon's stop time is garbage", func(t *testing.T) {
		fresh := &timerServer{version: "new"}

		lost := restartOnto(t, &timerServer{version: "old", stopAt: "soon"}, fresh)

		require.Len(t, lost, 1)
		assert.Contains(t, lost[0].Error(), `"soon"`)
		assert.Empty(t, fresh.stopRequests())
	})
}

// TestEnsureServer_RestartOfAnIdleDaemonCarriesItsSleepTimer covers the
// restart a passive command triggers: a timer armed on a stopped daemon
// still applies to whatever is played before it fires.
func TestEnsureServer_RestartOfAnIdleDaemonCarriesItsSleepTimer(t *testing.T) {
	path := testSocketPath(t)
	(&timerServer{version: "old", stopAt: wireTime(time.Now().Add(10 * time.Minute))}).start(t, path)
	fresh := &timerServer{version: "new"}
	prev := spawnServer
	spawnServer = func() (<-chan error, error) {
		fresh.start(t, path)
		return nil, nil
	}
	t.Cleanup(func() { spawnServer = prev })

	c, hr, err := EnsureServer(UnixEndpoint(path), "new")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	assert.Equal(t, "new", hr.ServerVersion)
	require.Len(t, fresh.stopRequests(), 1)
}
