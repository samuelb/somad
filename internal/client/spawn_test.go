package client

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"somad/internal/protocol"
	"somad/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setLogDir points the state (and thus server log) directory at a temp dir.
func setLogDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// appendToServerLog writes text to the server log like a spawned server would.
func appendToServerLog(t *testing.T, text string) {
	t.Helper()
	logPath, err := state.GetLogFilePath()
	require.NoError(t, err)
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 // Test file path
	require.NoError(t, err)
	_, err = f.WriteString(text)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestOpenServerLog_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")

	f, err := openServerLog(path)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.FileExists(t, path)
}

func TestOpenServerLog_AppendsBelowCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))

	f, err := openServerLog(path)
	require.NoError(t, err)
	_, err = f.WriteString("new\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	data, err := os.ReadFile(path) // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "old\nnew\n", string(data))
}

func TestOpenServerLog_TruncatesAboveCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	oversized := bytes.Repeat([]byte("x"), maxServerLogSize+1)
	require.NoError(t, os.WriteFile(path, oversized, 0o600))

	f, err := openServerLog(path)
	require.NoError(t, err)
	_, err = f.WriteString("fresh\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	data, err := os.ReadFile(path) // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "fresh\n", string(data), "an oversized log must be truncated at spawn")
}

func TestServerLogSince_ReturnsOnlyNewOutput(t *testing.T) {
	setLogDir(t)
	appendToServerLog(t, "old line\n")

	offset := serverLogSize()
	appendToServerLog(t, "error initializing the audio player: boom\n")

	tail := serverLogSince(offset)
	assert.Equal(t, "error initializing the audio player: boom", tail)
}

func TestServerLogSince_TruncatedLogReturnsAll(t *testing.T) {
	setLogDir(t)
	appendToServerLog(t, "fresh output\n")

	// An offset past EOF means the spawn truncated an oversized log; the
	// whole file is then the new server's output.
	tail := serverLogSince(1 << 30)
	assert.Equal(t, "fresh output", tail)
}

func TestServerLogSince_MissingLogIsEmpty(t *testing.T) {
	setLogDir(t)
	assert.Empty(t, serverLogSince(0))
}

func TestServerLogSince_CapsLines(t *testing.T) {
	setLogDir(t)
	for i := 1; i <= maxLogTailLines+5; i++ {
		appendToServerLog(t, fmt.Sprintf("line %d\n", i))
	}

	tail := serverLogSince(0)
	lines := strings.Split(tail, "\n")
	require.Len(t, lines, maxLogTailLines)
	assert.Equal(t, "line 6", lines[0], "only the last lines must be quoted")
	assert.Equal(t, fmt.Sprintf("line %d", maxLogTailLines+5), lines[len(lines)-1])
}

func TestEnsureServer_SpawnFailureQuotesServerLog(t *testing.T) {
	setLogDir(t)
	path := testSocketPath(t)

	prevWait := spawnWait
	spawnWait = 300 * time.Millisecond
	t.Cleanup(func() { spawnWait = prevWait })

	prevSpawn := spawnServer
	spawnServer = func() (<-chan error, error) {
		// The "server" writes a complaint to the log, then hangs without
		// ever binding the socket.
		appendToServerLog(t, "error initializing the audio player: no device\n")
		return nil, nil
	}
	t.Cleanup(func() { spawnServer = prevSpawn })

	_, _, err := EnsureServer(UnixEndpoint(path), "dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not come up")
	assert.Contains(t, err.Error(), "server log:")
	assert.Contains(t, err.Error(), "no device")
}

// exitedWith returns a spawnServer exit channel for a daemon that has
// already exited with err (nil: cleanly).
func exitedWith(err error) <-chan error {
	ch := make(chan error, 1)
	ch <- err
	return ch
}

func TestEnsureServer_FailedStartEndsTheWaitAtOnce(t *testing.T) {
	setLogDir(t)
	path := testSocketPath(t)

	prevInterval := spawnRetryInterval
	spawnRetryInterval = 10 * time.Millisecond
	t.Cleanup(func() { spawnRetryInterval = prevInterval })

	prevSpawn := spawnServer
	var spawns atomic.Int32
	spawnServer = func() (<-chan error, error) {
		// Like log.Fatalf in the daemon: one line in the log, exit status 1.
		spawns.Add(1)
		appendToServerLog(t, "refusing to serve 0.0.0.0:59999 without authentication\n")
		return exitedWith(errors.New("exit status 1")), nil
	}
	t.Cleanup(func() { spawnServer = prevSpawn })

	start := time.Now()
	_, _, err := EnsureServer(UnixEndpoint(path), "dev")

	// A daemon that fails to start would fail the same way again: neither
	// wait out spawnWait nor re-spawn it, and quote its complaint once.
	require.Error(t, err)
	assert.Less(t, time.Since(start), spawnWait/2, "a failed start must end the wait at once")
	assert.Equal(t, int32(1), spawns.Load(), "a daemon that failed to start must not be spawned again")
	assert.Contains(t, err.Error(), "failed to start (exit status 1)")
	assert.Equal(t, 1, strings.Count(err.Error(), "refusing to serve"), "the log tail must be quoted once")
}

// incompatibleHandler answers like a daemon from another soma version: it
// rejects hello the way every soma daemon words a protocol mismatch and
// refuses every other request before a successful hello, shutdown included.
func incompatibleHandler(req protocol.Request, send func(v any)) {
	if req.Method == protocol.MethodHello {
		send(protocol.Response{ID: req.ID, Error: "incompatible protocol version: server speaks 1, client speaks 2"})
		return
	}
	send(protocol.Response{ID: req.ID, Error: fmt.Sprintf("hello required before %q", req.Method)})
}

// catchSIGTERM diverts SIGTERM for the rest of the test. The fake daemons
// live in the test process, so that is where a client's signal lands: onTerm
// runs in place of the default action (which would kill the test binary),
// and the returned channel closes once it has.
func catchSIGTERM(t *testing.T, onTerm func()) <-chan struct{} {
	t.Helper()
	terminated := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	go func() {
		if _, ok := <-sigCh; ok {
			onTerm()
			close(terminated)
		}
	}()
	t.Cleanup(func() {
		signal.Stop(sigCh)
		close(sigCh)
	})
	return terminated
}

// startIncompatibleServer runs an incompatibleHandler daemon on path that,
// like the real one, stops listening when it receives SIGTERM. The returned
// channel closes once it has.
func startIncompatibleServer(t *testing.T, path string) <-chan struct{} {
	t.Helper()
	fs := startFakeServer(t, path, incompatibleHandler)
	return catchSIGTERM(t, func() { _ = fs.ln.Close() })
}

// peerPIDSupported reports whether socketPeerPID is implemented here.
func peerPIDSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func TestEnsureServer_LeavesProtocolSkewedServerRunning(t *testing.T) {
	path := testSocketPath(t)
	terminated := startIncompatibleServer(t, path)

	prev := spawnServer
	spawned := false
	spawnServer = func() (<-chan error, error) { spawned = true; return nil, nil }
	t.Cleanup(func() { spawnServer = prev })

	_, _, err := EnsureServer(UnixEndpoint(path), "new")

	// A passive command cannot tell whether the old daemon is playing, so it
	// must neither stop nor replace it — but must say how to.
	var skew *ProtocolSkewError
	require.ErrorAs(t, err, &skew)
	if peerPIDSupported() {
		assert.Equal(t, os.Getpid(), skew.PID, "the PID must be the socket's peer, the daemon process")
	}
	assert.Contains(t, err.Error(), "incompatible protocol version")
	assert.Contains(t, err.Error(), "soma daemon stop")
	assert.False(t, spawned)
	select {
	case <-terminated:
		t.Fatal("a passive command must not stop a protocol-skewed daemon")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestEnsureServerForPlayback_ReplacesProtocolSkewedServer(t *testing.T) {
	if !peerPIDSupported() {
		t.Skip("reading a socket's peer PID is only implemented on macOS and Linux")
	}
	path := testSocketPath(t)
	terminated := startIncompatibleServer(t, path)

	prev := spawnServer
	spawnServer = func() (<-chan error, error) {
		startFakeServer(t, path, defaultHandler("new"))
		return nil, nil
	}
	t.Cleanup(func() { spawnServer = prev })

	c, hr, err := EnsureServerForPlayback(UnixEndpoint(path), "new")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	// The old daemon refuses the shutdown request, so it is signalled; the
	// command interrupts playback anyway, so a fresh spawn takes over.
	assert.Equal(t, "new", hr.ServerVersion)
	select {
	case <-terminated:
	case <-time.After(5 * time.Second):
		t.Fatal("the protocol-skewed daemon was not sent SIGTERM")
	}
}

func TestEnsureServerForPlayback_RemoteProtocolSkewIsNeverSignalled(t *testing.T) {
	addr := startFakeTCPServer(t, incompatibleHandler)
	terminated := catchSIGTERM(t, func() {})

	_, _, err := EnsureServerForPlayback(Endpoint{Network: "tcp", Address: addr}, "new")

	require.Error(t, err)
	var skew *ProtocolSkewError
	assert.NotErrorAs(t, err, &skew, "only a local daemon may be stopped for speaking another protocol")
	assert.Contains(t, err.Error(), "incompatible protocol version")
	select {
	case <-terminated:
		t.Fatal("a remote endpoint must never lead to a signal")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStopIncompatible_FailsWhenTheDaemonDoesNotExit(t *testing.T) {
	if !peerPIDSupported() {
		t.Skip("reading a socket's peer PID is only implemented on macOS and Linux")
	}
	path := testSocketPath(t)
	startFakeServer(t, path, incompatibleHandler)
	// A daemon that ignores the signal and keeps listening.
	catchSIGTERM(t, func() {})

	prevRestartWait := restartWait
	restartWait = 300 * time.Millisecond
	t.Cleanup(func() { restartWait = prevRestartWait })

	ep := UnixEndpoint(path)
	_, _, err := EnsureServer(ep, "new")
	var skew *ProtocolSkewError
	require.ErrorAs(t, err, &skew)

	err = StopIncompatible(skew, ep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not exit")
}
