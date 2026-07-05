package client

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"somatui/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSocketPath returns a socket path short enough for sun_path limits.
func testSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "somatui")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// fakeServer is a scripted protocol server for exercising the client.
type fakeServer struct {
	t  *testing.T
	ln net.Listener
	// handle returns the response for a request; nil means no response.
	handle func(req protocol.Request, send func(v any))
}

func startFakeServer(t *testing.T, path string, handle func(req protocol.Request, send func(v any))) *fakeServer {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	fs := &fakeServer{t: t, ln: ln, handle: handle}
	go fs.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return fs
}

func (fs *fakeServer) acceptLoop() {
	for {
		nc, err := fs.ln.Accept()
		if err != nil {
			return
		}
		go fs.serve(nc)
	}
}

func (fs *fakeServer) serve(nc net.Conn) {
	defer func() { _ = nc.Close() }()
	send := func(v any) { _ = protocol.WriteLine(nc, v) }
	sc := protocol.NewScanner(nc)
	for sc.Scan() {
		var req protocol.Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		fs.handle(req, send)
	}
}

// defaultHandler answers hello and status like a healthy server.
func defaultHandler(serverVersion string) func(req protocol.Request, send func(v any)) {
	return func(req protocol.Request, send func(v any)) {
		respond := func(result any) {
			raw, _ := json.Marshal(result)
			send(protocol.Response{ID: req.ID, Result: raw})
		}
		switch req.Method {
		case protocol.MethodHello:
			respond(protocol.HelloResult{ServerVersion: serverVersion, ProtocolVersion: protocol.Version, PID: 42})
		case protocol.MethodStatus:
			respond(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})
		case protocol.MethodPlay:
			respond(protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad", Volume: 1})
		default:
			send(protocol.Response{ID: req.ID, Error: "unknown method"})
		}
	}
}

func TestClient_CallCorrelation(t *testing.T) {
	path := testSocketPath(t)
	startFakeServer(t, path, defaultHandler("dev"))

	c, err := Dial(path)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	hr, err := c.Hello("dev")
	require.NoError(t, err)
	assert.Equal(t, "dev", hr.ServerVersion)

	st, err := c.Play("groovesalad")
	require.NoError(t, err)
	assert.Equal(t, protocol.StatusPlaying, st.Status)

	_, err = c.ToggleFavorite("x")
	assert.ErrorContains(t, err, "unknown method")
}

func TestClient_EventsDeliveredAndDecoded(t *testing.T) {
	path := testSocketPath(t)
	startFakeServer(t, path, func(req protocol.Request, send func(v any)) {
		defaultHandler("dev")(req, send)
		if req.Method == protocol.MethodStatus {
			stEv, _ := protocol.NewEvent(protocol.EventState, protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "dronezone", Volume: 0.5})
			send(stEv)
			chEv, _ := protocol.NewEvent(protocol.EventChannels, protocol.ChannelsPayload{Favorites: []string{"dronezone"}})
			send(chEv)
		}
	})

	c, err := Dial(path)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	_, err = c.Hello("dev")
	require.NoError(t, err)
	_, err = c.Status()
	require.NoError(t, err)

	var gotState, gotChannels bool
	deadline := time.After(5 * time.Second)
	for !gotState || !gotChannels {
		select {
		case ev, ok := <-c.Events():
			require.True(t, ok, "events channel closed early")
			switch v := ev.(type) {
			case protocol.PlaybackState:
				assert.Equal(t, "dronezone", v.ChannelID)
				gotState = true
			case protocol.ChannelsPayload:
				assert.Equal(t, []string{"dronezone"}, v.Favorites)
				gotChannels = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for events")
		}
	}
}

func TestClient_EventsChannelClosesOnDisconnect(t *testing.T) {
	path := testSocketPath(t)
	fs := startFakeServer(t, path, defaultHandler("dev"))

	c, err := Dial(path)
	require.NoError(t, err)
	_, err = c.Hello("dev")
	require.NoError(t, err)

	_ = fs.ln.Close() // stop accepting; close established conns via server side
	_ = c.nc.Close()  // simulate the server dropping the connection

	select {
	case _, ok := <-c.Events():
		assert.False(t, ok, "events channel should close on disconnect")
	case <-time.After(5 * time.Second):
		t.Fatal("events channel did not close")
	}

	_, err = c.Status()
	assert.ErrorIs(t, err, ErrDisconnected)
}

func TestEnsureServer_SpawnsAndRetries(t *testing.T) {
	path := testSocketPath(t)

	prev := spawnServer
	spawnServer = func() error {
		// The "server" comes up only after a delay, like a real spawn.
		go func() {
			time.Sleep(300 * time.Millisecond)
			startFakeServer(t, path, defaultHandler("dev"))
		}()
		return nil
	}
	t.Cleanup(func() { spawnServer = prev })

	c, hr, err := EnsureServer(path, "dev")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	assert.Equal(t, "dev", hr.ServerVersion)
}

// startOutdatedServer runs a fake server that reports the given version and
// playback status and, when asked to shut down, stops listening so a
// replacement can bind the socket. It records the channel of any resume Play.
func startOutdatedServer(t *testing.T, path, serverVersion, status, channelID string, played chan<- string) {
	t.Helper()
	var ln net.Listener
	fs := startFakeServer(t, path, func(req protocol.Request, send func(v any)) {
		respond := func(result any) {
			raw, _ := json.Marshal(result)
			send(protocol.Response{ID: req.ID, Result: raw})
		}
		switch req.Method {
		case protocol.MethodHello:
			respond(protocol.HelloResult{ServerVersion: serverVersion, ProtocolVersion: protocol.Version})
		case protocol.MethodStatus:
			respond(protocol.PlaybackState{Status: status, ChannelID: channelID, Volume: 1})
		case protocol.MethodPlay:
			var p protocol.PlayParams
			_ = json.Unmarshal(req.Params, &p)
			select {
			case played <- p.ChannelID:
			default:
			}
			respond(protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: p.ChannelID, Volume: 1})
		case protocol.MethodShutdown:
			respond(struct{}{})
			// Closing the listener unlinks the socket so the replacement can
			// take it; waitForServerExit then observes the exit.
			_ = ln.Close()
		}
	})
	ln = fs.ln
}

func TestEnsureServer_RestartsPlayingServerAndResumes(t *testing.T) {
	path := testSocketPath(t)
	oldPlayed := make(chan string, 1) // the old server never gets a Play
	startOutdatedServer(t, path, "old", protocol.StatusPlaying, "groovesalad", oldPlayed)

	resumed := make(chan string, 1)
	prev := spawnServer
	spawnServer = func() error {
		startOutdatedServer(t, path, "new", protocol.StatusStopped, "", resumed)
		return nil
	}
	t.Cleanup(func() { spawnServer = prev })

	c, hr, err := EnsureServer(path, "new")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// The outdated server is replaced by one on our version...
	assert.Equal(t, "new", hr.ServerVersion)
	// ...and whatever it was playing resumes on the replacement.
	select {
	case ch := <-resumed:
		assert.Equal(t, "groovesalad", ch, "playback must resume on the restarted server")
	case <-time.After(2 * time.Second):
		t.Fatal("restarted server was never asked to resume playback")
	}
}

func TestEnsureServer_RestartsIdleServerWithoutResuming(t *testing.T) {
	path := testSocketPath(t)
	startOutdatedServer(t, path, "old", protocol.StatusStopped, "", make(chan string, 1))

	resumed := make(chan string, 1)
	prev := spawnServer
	spawnServer = func() error {
		startOutdatedServer(t, path, "new", protocol.StatusStopped, "", resumed)
		return nil
	}
	t.Cleanup(func() { spawnServer = prev })

	c, hr, err := EnsureServer(path, "new")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	assert.Equal(t, "new", hr.ServerVersion)
	select {
	case ch := <-resumed:
		t.Fatalf("a stopped server must not trigger a resume, but %q was played", ch)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestEnsureServer_ErrorsWhenStaleServerWontExit(t *testing.T) {
	path := testSocketPath(t)
	// A stubborn old server: answers Shutdown but keeps listening, as if it
	// ignored the request or is slow to tear down.
	startFakeServer(t, path, func(req protocol.Request, send func(v any)) {
		respond := func(result any) {
			raw, _ := json.Marshal(result)
			send(protocol.Response{ID: req.ID, Result: raw})
		}
		switch req.Method {
		case protocol.MethodHello:
			respond(protocol.HelloResult{ServerVersion: "old", ProtocolVersion: protocol.Version})
		case protocol.MethodStatus:
			respond(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 1})
		case protocol.MethodShutdown:
			respond(struct{}{})
		}
	})

	prevRestartWait := restartWait
	restartWait = 300 * time.Millisecond
	t.Cleanup(func() { restartWait = prevRestartWait })

	prev := spawnServer
	spawnServer = func() error { return nil }
	t.Cleanup(func() { spawnServer = prev })

	_, _, err := EnsureServer(path, "new")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not exit")
}
