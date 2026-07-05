package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"somatui/internal/protocol"
	"somatui/internal/state"
)

const (
	dialRetryInterval = 100 * time.Millisecond
	spawnWait         = 15 * time.Second
	restartWait       = 3 * time.Second
)

// spawnServer is a variable so tests can fake the server launch.
var spawnServer = SpawnServer

// SpawnServer starts a detached `somatui server` process using the current
// executable, with its stderr appended to the server log file.
func SpawnServer() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate executable: %w", err)
	}

	// context.Background: the server must outlive us, so it is never cancelled.
	cmd := exec.CommandContext(context.Background(), exe, "server") // #nosec G204 -- os.Executable, not user input
	// A new session detaches the server from our terminal so it survives the
	// client (and the terminal) going away.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if logPath, err := state.GetLogFilePath(); err == nil {
		if logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil { // #nosec G304 -- path derived from state dir
			cmd.Stderr = logFile
			defer func() { _ = logFile.Close() }() // child keeps its own descriptor
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start somatui server: %w", err)
	}
	return cmd.Process.Release()
}

// EnsureServer returns a connected, hello-verified client, spawning the
// server when none is running. When the running server's binary version
// differs from ours and it is idle, it is restarted transparently; when it
// is playing, the session is kept and the caller can warn using the
// returned HelloResult.
func EnsureServer(socketPath, clientVersion string) (*Client, protocol.HelloResult, error) {
	c, hr, err := connectOrSpawn(socketPath, clientVersion)
	if err != nil {
		return nil, hr, err
	}

	if hr.ServerVersion != clientVersion {
		if st, err := c.Status(); err == nil && st.Status == protocol.StatusStopped {
			_ = c.Shutdown()
			_ = c.Close()
			waitForServerExit(socketPath)
			return connectOrSpawn(socketPath, clientVersion)
		}
	}
	return c, hr, nil
}

// connectOrSpawn dials the socket, spawning a server and retrying when
// nothing answers, then performs the hello handshake.
func connectOrSpawn(socketPath, clientVersion string) (*Client, protocol.HelloResult, error) {
	c, err := Dial(socketPath)
	if err != nil {
		if err := spawnServer(); err != nil {
			return nil, protocol.HelloResult{}, err
		}
		deadline := time.Now().Add(spawnWait)
		for {
			c, err = Dial(socketPath)
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				return nil, protocol.HelloResult{}, fmt.Errorf(
					"somatui server did not come up on %s: %w", socketPath, err)
			}
			time.Sleep(dialRetryInterval)
		}
	}

	hr, err := c.Hello(clientVersion)
	if err != nil {
		_ = c.Close()
		return nil, hr, fmt.Errorf("handshake with somatui server failed: %w", err)
	}
	return c, hr, nil
}

// waitForServerExit waits until the old server has stopped answering the
// socket, so a fresh spawn doesn't lose the single-instance race to it.
func waitForServerExit(socketPath string) {
	deadline := time.Now().Add(restartWait)
	for time.Now().Before(deadline) {
		c, err := Dial(socketPath)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(dialRetryInterval)
	}
}

// IsNotRunning reports whether err looks like "no server is listening",
// as opposed to a protocol or handshake failure.
func IsNotRunning(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}
