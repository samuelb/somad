package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"somad/internal/protocol"
	"somad/internal/state"
)

const (
	dialRetryInterval = 100 * time.Millisecond

	// maxServerLogSize caps server.log: spawns append to it and it would
	// otherwise grow forever, so a spawn that finds it above the cap
	// truncates it first.
	maxServerLogSize = 1 << 20 // 1 MiB

	// maxLogTailLines bounds how much of the server log a startup error
	// message quotes.
	maxLogTailLines = 10
)

// spawnWait is how long a spawned server gets to bind its socket. A variable
// so tests can shrink it.
var spawnWait = 15 * time.Second

// restartWait is how long an old server gets to exit before a version-skew
// restart gives up. A variable so tests can shrink it.
var restartWait = 3 * time.Second

// spawnServer is a variable so tests can fake the server launch.
var spawnServer = SpawnServer

// SpawnServer starts a detached `soma daemon` process using the current
// executable, with its stderr appended to the server log file.
func SpawnServer() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to locate executable: %w", err)
	}

	// context.Background: the server must outlive us, so it is never cancelled.
	cmd := exec.CommandContext(context.Background(), exe, "daemon") // #nosec G204 -- os.Executable, not user input
	// A new session detaches the server from our terminal so it survives the
	// client (and the terminal) going away.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if logPath, err := state.GetLogFilePath(); err == nil {
		if logFile, err := openServerLog(logPath); err == nil {
			cmd.Stderr = logFile
			defer func() { _ = logFile.Close() }() // child keeps its own descriptor
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start soma daemon: %w", err)
	}
	return cmd.Process.Release()
}

// openServerLog opens the server log for appending, truncating it first
// once it has outgrown maxServerLogSize.
func openServerLog(path string) (*os.File, error) {
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if info, err := os.Stat(path); err == nil && info.Size() > maxServerLogSize {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600) // #nosec G304 -- path derived from state dir
}

// EnsureServer returns a connected, hello-verified client, spawning the server
// when none is running. A version-skewed server is restarted onto our binary
// only when it is idle, so music that is playing is never cut off just to
// upgrade the daemon — callers about to interrupt playback anyway use
// EnsureServerForPlayback instead.
func EnsureServer(socketPath, clientVersion string) (*Client, protocol.HelloResult, error) {
	c, hr, err := connectOrSpawn(socketPath, clientVersion)
	if err != nil {
		return nil, hr, err
	}
	if hr.ServerVersion == clientVersion {
		return c, hr, nil
	}
	// Skewed but playing: leave it be. Restarting would interrupt the stream,
	// and the running binary still speaks our protocol version.
	if st, err := c.Status(); err != nil || st.Status != protocol.StatusStopped {
		return c, hr, nil
	}
	nc, nhr, err := Restart(c, socketPath, clientVersion)
	if err != nil {
		return nil, nhr, err
	}
	return nc, nhr, nil
}

// EnsureServerForPlayback is EnsureServer for callers about to change, pause,
// or stop the stream: because that interrupts playback anyway, a version-skewed
// server is restarted onto our binary even while it is playing. The interrupting
// command that follows establishes the new playback state, so nothing is
// resumed here.
func EnsureServerForPlayback(socketPath, clientVersion string) (*Client, protocol.HelloResult, error) {
	c, hr, err := connectOrSpawn(socketPath, clientVersion)
	if err != nil {
		return nil, hr, err
	}
	if hr.ServerVersion == clientVersion {
		return c, hr, nil
	}
	return Restart(c, socketPath, clientVersion)
}

// Restart shuts the connected server down and returns a client to a fresh spawn
// of our own binary. It closes c on the way. Callers use it only when the
// running server is out of date and the operation about to follow interrupts
// playback anyway; the persisted volume and last-played channel carry across on
// their own, so the caller need only re-establish the stream it wants.
func Restart(c *Client, socketPath, clientVersion string) (*Client, protocol.HelloResult, error) {
	_ = c.Shutdown()
	_ = c.Close()
	if !waitForServerExit(socketPath) {
		return nil, protocol.HelloResult{}, fmt.Errorf("soma daemon did not exit within %s to restart as version %s", restartWait, clientVersion)
	}
	return connectOrSpawn(socketPath, clientVersion)
}

// connectOrSpawn dials the socket, spawning a server and retrying when
// nothing answers, then performs the hello handshake.
func connectOrSpawn(socketPath, clientVersion string) (*Client, protocol.HelloResult, error) {
	c, err := Dial(socketPath)
	if err != nil {
		// Remember where the log ends now, so a startup failure can quote
		// exactly what the new server wrote.
		logOffset := serverLogSize()
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
				spawnErr := fmt.Errorf("soma daemon did not come up on %s: %w", socketPath, err)
				// The failure reason lives in the server's log; without it
				// the user only learns "did not come up".
				if tail := serverLogSince(logOffset); tail != "" {
					spawnErr = fmt.Errorf("%w\nserver log:\n%s", spawnErr, tail)
				}
				return nil, protocol.HelloResult{}, spawnErr
			}
			time.Sleep(dialRetryInterval)
		}
	}

	hr, err := c.Hello(clientVersion)
	if err != nil {
		_ = c.Close()
		return nil, hr, fmt.Errorf("handshake with soma daemon failed: %w", err)
	}
	return c, hr, nil
}

// serverLogSize returns the server log's current size, so output written by
// a subsequent spawn can be told apart from older log content.
func serverLogSize() int64 {
	logPath, err := state.GetLogFilePath()
	if err != nil {
		return 0
	}
	info, err := os.Stat(logPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

// serverLogSince returns what the server wrote to its log after offset,
// capped to the last maxLogTailLines lines, for startup error messages.
// It returns "" when there is nothing to quote.
func serverLogSince(offset int64) string {
	logPath, err := state.GetLogFilePath()
	if err != nil {
		return ""
	}
	f, err := os.Open(logPath) // #nosec G304 -- path derived from state dir
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()

	// The spawn may have truncated an oversized log; then the whole file is
	// the new server's output.
	if info, err := f.Stat(); err == nil && offset > info.Size() {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxLogTailLines {
		lines = lines[len(lines)-maxLogTailLines:]
	}
	return strings.Join(lines, "\n")
}

// waitForServerExit waits until the old server has stopped answering the
// socket, so a fresh spawn doesn't lose the single-instance race to it. It
// reports whether the server exited before restartWait elapsed.
func waitForServerExit(socketPath string) bool {
	deadline := time.Now().Add(restartWait)
	for time.Now().Before(deadline) {
		c, err := Dial(socketPath)
		if err != nil {
			return true
		}
		_ = c.Close()
		time.Sleep(dialRetryInterval)
	}
	return false
}
