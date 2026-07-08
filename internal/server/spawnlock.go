package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"somad/internal/protocol"
)

// ErrAlreadyRunning reports that another server instance holds the lock.
// Auto-spawn races resolve through it: the losing server exits cleanly and
// the clients that spawned both end up connecting to the winner.
var ErrAlreadyRunning = errors.New("soma daemon already running")

// Listen acquires the single-instance lock and listens on the Unix socket,
// removing any stale socket file left by a crashed server. The returned
// cleanup closes the listener, removes the socket, and releases the lock.
func Listen(socketPath string) (net.Listener, func(), error) {
	if err := protocol.EnsureSocketDir(socketPath); err != nil {
		return nil, nil, fmt.Errorf("failed to create socket directory: %w", err)
	}

	lockFile, err := os.OpenFile(protocol.LockPath(socketPath), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- path derived from trusted socket path
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open lock file: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lockFile.Close()
		return nil, nil, ErrAlreadyRunning
	}

	// We hold the lock, so any existing socket file is stale — remove it.
	_ = os.Remove(socketPath)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socketPath)
	if err != nil {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		return nil, nil, fmt.Errorf("failed to listen on %s: %w", socketPath, err)
	}

	cleanup := func() {
		_ = ln.Close()
		_ = os.Remove(socketPath)
		// The lock file itself stays behind: unlinking it would race with a
		// new server locking the same path.
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
	}
	return ln, cleanup, nil
}
