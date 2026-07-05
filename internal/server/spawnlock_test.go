package server

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSocketPath returns a socket path short enough for sun_path limits.
func testSocketPath(t *testing.T) string {
	t.Helper()
	// t.TempDir() can exceed the 104-byte sun_path limit on macOS.
	dir, err := os.MkdirTemp("", "somatui")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func TestListen_SecondInstanceRejected(t *testing.T) {
	path := testSocketPath(t)

	ln, cleanup, err := Listen(path)
	require.NoError(t, err)
	defer cleanup()
	require.NotNil(t, ln)

	_, _, err = Listen(path)
	assert.ErrorIs(t, err, ErrAlreadyRunning)
}

func TestListen_RemovesStaleSocket(t *testing.T) {
	path := testSocketPath(t)

	// Simulate a crashed server: socket file exists, no lock held.
	stale, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	// Closing a unix listener removes its socket file; recreate it to fake
	// the aftermath of a kill -9.
	require.NoError(t, stale.Close())
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	ln, cleanup, err := Listen(path)
	require.NoError(t, err)
	defer cleanup()
	assert.NotNil(t, ln)
}

func TestListen_CleanupAllowsRestart(t *testing.T) {
	path := testSocketPath(t)

	_, cleanup, err := Listen(path)
	require.NoError(t, err)
	cleanup()

	_, err = os.Stat(path)
	assert.True(t, errors.Is(err, os.ErrNotExist), "socket file should be removed on cleanup")

	ln, cleanup2, err := Listen(path)
	require.NoError(t, err)
	defer cleanup2()
	assert.NotNil(t, ln)
}
