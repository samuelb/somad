package client

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"somatui/internal/state"

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
	spawnServer = func() error {
		// The "server" writes its dying words to the log and never binds
		// the socket.
		appendToServerLog(t, "error initializing the audio player: no device\n")
		return nil
	}
	t.Cleanup(func() { spawnServer = prevSpawn })

	_, _, err := EnsureServer(path, "dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not come up")
	assert.Contains(t, err.Error(), "server log:")
	assert.Contains(t, err.Error(), "no device")
}
