package client

import (
	"context"
	"errors"
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

// spawnRetryInterval is how long after a spawn connectOrSpawn repeats it when
// the spawned daemon exited cleanly without the socket coming up. A freshly
// spawned daemon can lose the single-instance lock to an old one still
// finishing its shutdown: the old daemon closes its listeners first (which
// is what a client sees as "gone") and releases the lock only after flushing
// state and waiting for pending Last.fm submissions. The new daemon then
// exits at once with "already running" (status 0), and a single spawn would
// leave the client polling a dead socket for the whole spawnWait. Repeating
// the spawn covers that gap at no cost: concurrent spawns are safe by
// construction (ADR-0004). A daemon that exits non-zero failed to start, and
// a repeat would only fail the same way, so that ends the wait instead.
// A variable so tests can shrink it.
var spawnRetryInterval = time.Second

// restartWait is how long an old server gets to exit before a version-skew
// restart gives up and falls back to the still-running server. A variable so
// tests can shrink it.
var restartWait = 5 * time.Second

// spawnServer is a variable so tests can fake the server launch.
var spawnServer = SpawnServer

// SetSpawnServer replaces how a missing local daemon is started and returns
// a function that puts the real launch back. It exists for the tests of
// code built on this package (cmd/soma): their daemons are fakes inside the
// test process, and the real launch would run the test binary as `soma
// daemon`. With it they go through the real spawn, restart and
// version-skew logic, and only the process start is faked.
func SetSpawnServer(spawn func() (<-chan error, error)) (restore func()) {
	prev := spawnServer
	spawnServer = spawn
	return func() { spawnServer = prev }
}

// SpawnServer starts a detached `soma daemon` process using the current
// executable, with its stderr appended to the server log file. The returned
// channel receives the process's exit (nil when clean) once it ends: a
// daemon that cannot start exits non-zero, while one that finds another
// already running exits cleanly.
func SpawnServer() (<-chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("failed to locate executable: %w", err)
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
		return nil, fmt.Errorf("failed to start soma daemon: %w", err)
	}
	// Waiting rather than releasing the process reports a failed start the
	// moment it happens, and reaps the daemon should it exit while we run.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
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
//
// A local server that speaks another protocol version cannot even report
// whether it is playing, so it too is left running: the caller gets a
// *ProtocolSkewError telling the user how to replace it.
//
// A remote (TCP) endpoint is never spawned or restarted: an unreachable one
// is an error, and a version-skewed one is left alone (the hello handshake
// already guarantees it speaks our protocol version).
func EnsureServer(ep Endpoint, clientVersion string) (*Client, protocol.HelloResult, error) {
	return ensureServer(ep, clientVersion, false)
}

// EnsureServerForPlayback is EnsureServer for callers about to change, pause,
// or stop the stream: because that interrupts playback anyway, a version-skewed
// local server is restarted onto our binary even while it is playing, and one
// that speaks another protocol version is replaced (see RestartIncompatible).
// The interrupting command that follows establishes the new playback state,
// so nothing is resumed here.
func EnsureServerForPlayback(ep Endpoint, clientVersion string) (*Client, protocol.HelloResult, error) {
	return ensureServer(ep, clientVersion, true)
}

// Connect returns a connected, hello-verified client, spawning the server
// when none is running, and leaves a version-skewed one exactly as it is,
// playing or idle. It is for a caller that restarts such a server itself
// (NeedsRestart, Restart), but only once it knows that the command about to
// interrupt playback will go ahead. A local server that speaks another
// protocol version comes back as a *ProtocolSkewError, as from EnsureServer.
func Connect(ep Endpoint, clientVersion string) (*Client, protocol.HelloResult, error) {
	return connectOrSpawn(ep, clientVersion)
}

func ensureServer(ep Endpoint, clientVersion string, restartWhilePlaying bool) (*Client, protocol.HelloResult, error) {
	c, hr, err := connectOrSpawn(ep, clientVersion)
	var skew *ProtocolSkewError
	if errors.As(err, &skew) && restartWhilePlaying {
		return RestartIncompatible(skew, ep, clientVersion)
	}
	if err != nil {
		return nil, hr, err
	}
	if !NeedsRestart(ep, clientVersion, hr.ServerVersion) {
		return c, hr, nil
	}
	if !restartWhilePlaying {
		// Skewed but playing: leave it be. Restarting would interrupt the
		// stream, and the running binary still speaks our protocol version.
		if st, err := c.Status(); err != nil || st.Status != protocol.StatusStopped {
			return c, hr, nil
		}
	}
	return Restart(c, ep, clientVersion)
}

// NeedsRestart reports whether a connected server should be restarted onto
// this binary: only a local one, and only when the versions differ. A remote
// server is never restarted, and a version-skewed one is tolerated (the
// hello handshake already checked that it speaks our protocol version).
func NeedsRestart(ep Endpoint, clientVersion, serverVersion string) bool {
	return ep.IsLocal() && VersionSkewed(clientVersion, serverVersion)
}

// Restart shuts the connected server down and returns a client to a fresh spawn
// of our own binary. It closes c on the way. Callers use it only when the
// running server is out of date and the operation about to follow interrupts
// playback anyway; the persisted volume and last-played channel carry across on
// their own, so the caller need only re-establish the stream it wants.
//
// A pending sleep timer (`soma stop --in`) lives only in the old server's
// memory, so Restart carries it: it asks the old server when the timer
// fires and arms the new one for what is left. A caller whose command ends
// playback (stop, pause) then cancels it like any other timer, by stopping.
// When it cannot be carried, the restart still succeeds and
// OnSleepTimerLost is told.
func Restart(c *Client, ep Endpoint, clientVersion string) (*Client, protocol.HelloResult, error) {
	if !ep.IsLocal() {
		return nil, protocol.HelloResult{}, fmt.Errorf("cannot restart the remote soma daemon at %s from here", ep)
	}
	stopAt, askErr := pendingStop(c)
	_ = c.Shutdown()
	_ = c.Close()
	// When the old server is slow to exit (a loaded machine, a long fade-out),
	// the user's command outranks the upgrade: fall through and talk to
	// whatever answers — connectOrSpawn reaches the old server while it still
	// listens (the handshake already guaranteed it speaks our protocol
	// version) and spawns a fresh one once it is gone. A later interrupting
	// command retries the upgrade.
	_ = waitForServerExit(ep)
	nc, hr, err := connectOrSpawn(ep, clientVersion)
	if err != nil {
		return nil, hr, err
	}
	carrySleepTimer(nc, stopAt, askErr)
	return nc, hr, nil
}

// OnSleepTimerLost is called when a restart could not carry the old
// daemon's sleep timer over to the new one (see Restart). The command that
// caused the restart goes ahead, but the user has to hear that playback
// will no longer stop on its own. By default that is a warning on stderr;
// the TUI, which owns the terminal, shows it in its status bar instead.
var OnSleepTimerLost = func(err error) {
	fmt.Fprintf(os.Stderr, "soma: warning: the sleep timer was lost in the daemon restart: %v; "+
		"arm it again with `soma stop --in <duration>`\n", err)
}

// pendingStop asks a daemon when its sleep timer will stop playback. The
// zero time means none is pending.
func pendingStop(c *Client) (time.Time, error) {
	st, err := c.Status()
	if err != nil {
		return time.Time{}, fmt.Errorf("the old daemon could not be asked whether one was pending: %w", err)
	}
	if st.StopAt == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, st.StopAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("the old daemon's stop time %q cannot be read: %w", st.StopAt, err)
	}
	return at, nil
}

// carrySleepTimer arms on a freshly restarted daemon the sleep timer its
// predecessor had pending for stopAt (see pendingStop, whose error is
// askErr), with the time that is left now. A timer already due is dropped:
// the old daemon's stop and this restart came to the same thing, and the
// command about to run starts playback knowingly. Should the restart have
// fallen back to the old daemon, this replaces its timer with an equal one.
func carrySleepTimer(nc *Client, stopAt time.Time, askErr error) {
	if askErr != nil {
		OnSleepTimerLost(askErr)
		return
	}
	if stopAt.IsZero() {
		return
	}
	remaining := time.Until(stopAt)
	if remaining <= 0 {
		return
	}
	if _, err := nc.StopIn(remaining); err != nil {
		OnSleepTimerLost(fmt.Errorf("the new daemon did not take it over (it was to stop playback at %s): %w",
			stopAt.Local().Format("15:04"), err))
	}
}

// connectOrSpawn dials the endpoint, spawning a local server and retrying
// when nothing answers, then performs the hello handshake. Remote endpoints
// are dialed only; nothing can spawn a server on another machine.
func connectOrSpawn(ep Endpoint, clientVersion string) (*Client, protocol.HelloResult, error) {
	c, err := DialEndpoint(ep)
	if err != nil {
		// Something answers, and the error says what is wrong with it. For
		// a remote daemon that is a pairing mistake (TLS on one side only,
		// the wrong certificate or key), not a daemon to go and start. For
		// a local one it is another user's (ErrForeignDaemon): a spawn
		// could not take the socket over (the directory or the lock file
		// next to it belongs to that other user too) and would only bury
		// this error under its own failure to start.
		if answered(err) {
			return nil, protocol.HelloResult{}, err
		}
		if !ep.IsLocal() {
			return nil, protocol.HelloResult{}, fmt.Errorf(
				"cannot reach the soma daemon at %s (remote servers are not started automatically; run `soma daemon` there): %w", ep, err)
		}
		// Remember where the log ends now, so a startup failure can quote
		// exactly what the new server wrote.
		logOffset := serverLogSize()
		var exited <-chan error
		if exited, err = spawnServer(); err != nil {
			return nil, protocol.HelloResult{}, err
		}
		lastSpawn := time.Now()
		deadline := lastSpawn.Add(spawnWait)
		running := true // the latest spawn has not exited yet
		for {
			c, err = DialEndpoint(ep)
			if err == nil {
				break
			}
			if answered(err) {
				return nil, protocol.HelloResult{}, err
			}
			if running {
				select {
				case exitErr := <-exited:
					if exitErr != nil {
						return nil, protocol.HelloResult{}, withServerLog(
							fmt.Errorf("soma daemon failed to start (%w)", exitErr), logOffset)
					}
					running = false
				default:
				}
			}
			now := time.Now()
			if now.After(deadline) {
				return nil, protocol.HelloResult{}, withServerLog(
					fmt.Errorf("soma daemon did not come up on %s: %w", ep.Address, err), logOffset)
			}
			if !running && now.Sub(lastSpawn) >= spawnRetryInterval {
				if exited, err = spawnServer(); err != nil {
					return nil, protocol.HelloResult{}, err
				}
				lastSpawn, running = now, true
			}
			time.Sleep(dialRetryInterval)
		}
	}

	hr, err := Handshake(c, ep, clientVersion)
	if err != nil {
		return nil, hr, err
	}
	return c, hr, nil
}

// answered reports whether a DialEndpoint error came from a daemon that
// accepted the connection (a *HandshakeError), as opposed to nothing being
// reachable at the endpoint.
func answered(err error) bool {
	var he *HandshakeError
	return errors.As(err, &he)
}

// protocolMismatchText is how every soma daemon since the client-server
// split words its hello rejection of a client speaking another protocol
// version. Matching on it is safe because the daemons that need recognizing
// are the older builds, whose wording is fixed.
const protocolMismatchText = "incompatible protocol version"

// ProtocolSkewError reports a local daemon that rejected our hello because
// it speaks another protocol version: a different soma install, older or
// newer, is still running. Unlike a daemon that is merely version-skewed it
// is unusable, and it refuses every request before a successful hello,
// shutdown included, so the only way to stop it is to signal its process.
type ProtocolSkewError struct {
	// PID is the daemon's process, from the socket's peer credentials; 0
	// when they could not be read, and PIDErr says why.
	PID    int
	PIDErr error
	// Err is the daemon's rejection of our hello.
	Err error
}

func (e *ProtocolSkewError) Error() string {
	who := "the running soma daemon"
	if e.PID > 0 {
		who = fmt.Sprintf("the running soma daemon (pid %d)", e.PID)
	}
	return fmt.Sprintf("%s belongs to another soma version (%v); it is left running so its music is not cut off: "+
		"`soma play` or `soma stop` restarts it onto this version, `soma daemon stop` just stops it", who, e.Err)
}

func (e *ProtocolSkewError) Unwrap() error { return e.Err }

// Handshake performs the hello handshake on a freshly dialed connection,
// closing it on failure. A local daemon that rejects our protocol version
// comes back as a *ProtocolSkewError, any other failure as a
// *HandshakeError.
func Handshake(c *Client, ep Endpoint, clientVersion string) (protocol.HelloResult, error) {
	hr, err := c.Hello(clientVersion)
	if err == nil {
		return hr, nil
	}
	defer func() { _ = c.Close() }()
	if ep.IsLocal() && strings.Contains(err.Error(), protocolMismatchText) {
		pid, pidErr := peerPID(c.nc)
		return hr, &ProtocolSkewError{PID: pid, PIDErr: pidErr, Err: err}
	}
	// Without a pre-shared key, hello is the connection's first round trip.
	return hr, &HandshakeError{Op: "handshake with soma daemon failed", Err: err, Hint: plaintextHint(ep, err)}
}

// terminateProcess sends pid SIGTERM, which a soma daemon handles like a
// shutdown request. A variable so tests, whose fake daemons live in the
// test process itself, can fake it.
var terminateProcess = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// StopIncompatible stops a local daemon that speaks another protocol version
// (see ProtocolSkewError) by signalling its process, and waits until it has
// let go of the socket and its lock. A remote daemon is never stopped this
// way.
func StopIncompatible(skew *ProtocolSkewError, ep Endpoint) error {
	if !ep.IsLocal() {
		return fmt.Errorf("cannot stop the remote soma daemon at %s from here", ep)
	}
	if skew.PID <= 0 {
		return fmt.Errorf("cannot stop the soma daemon on %s, which belongs to another soma version (%v): its process is unknown (%v); quit it from its tray icon or end it by hand",
			ep, skew.Err, skew.PIDErr)
	}
	if err := terminateProcess(skew.PID); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stopping the soma daemon (pid %d), which belongs to another soma version: %w", skew.PID, err)
	}
	if !waitForServerExit(ep) {
		return fmt.Errorf("the soma daemon (pid %d), which belongs to another soma version, did not exit within %s", skew.PID, restartWait)
	}
	return nil
}

// RestartIncompatible replaces a local daemon that speaks another protocol
// version with a fresh spawn of our binary, and returns a client to it. Like
// Restart it is only for a command about to interrupt playback anyway: the
// old daemon cannot say whether it is playing, so that is the only moment
// ADR-0006 leaves for the replacement. The fresh daemon starts stopped. Nor
// can the old daemon say whether a sleep timer is pending: unlike Restart,
// this carries none over, and a caller that goes on to play says so.
func RestartIncompatible(skew *ProtocolSkewError, ep Endpoint, clientVersion string) (*Client, protocol.HelloResult, error) {
	if err := StopIncompatible(skew, ep); err != nil {
		return nil, protocol.HelloResult{}, err
	}
	return connectOrSpawn(ep, clientVersion)
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

// withServerLog appends what the server logged after offset to a startup
// error: the failure reason lives there, and without it the user only
// learns that the daemon did not come up.
func withServerLog(err error, offset int64) error {
	if tail := serverLogSince(offset); tail != "" {
		return fmt.Errorf("%w\nserver log:\n%s", err, tail)
	}
	return err
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

// StopServer asks the connected daemon to shut down and, for a local one,
// returns only once it has let go of its socket and its single-instance
// lock (see waitForServerExit), so that whatever runs next can start a
// daemon: `soma daemon stop && soma daemon` found the lock still held by
// the daemon it had just stopped, took that for a running instance, and
// left none. A daemon that has not let go after restartWait is an error. A
// remote daemon cannot be watched from here; its answer to the request is
// all there is to wait for.
func StopServer(c *Client, ep Endpoint) error {
	if err := c.Shutdown(); err != nil {
		return err
	}
	if !ep.IsLocal() {
		return nil
	}
	if !waitForServerExit(ep) {
		return fmt.Errorf("the soma daemon on %s acknowledged the shutdown request but had not exited after %s", ep, restartWait)
	}
	return nil
}

// waitForServerExit waits until the old server has let go: it no longer
// answers the socket and has released the single-instance lock, so a
// daemon started next does not lose the single-instance race to it. Those
// are not the same moment. A daemon shutting down closes its listeners
// first and gives the lock up only after flushing its state and waiting, up
// to a few seconds, for a Last.fm submission still under way; one started
// in between finds the lock held, takes that for a running daemon, and
// exits. It reports whether the server let go before restartWait elapsed.
func waitForServerExit(ep Endpoint) bool {
	deadline := time.Now().Add(restartWait)
	for time.Now().Before(deadline) {
		c, err := DialEndpoint(ep)
		if err != nil && lockReleased(ep) {
			return true
		}
		if err == nil {
			_ = c.Close()
		}
		time.Sleep(dialRetryInterval)
	}
	return false
}

// lockReleased reports whether the single-instance lock of the daemon on a
// local endpoint is free: the flock internal/server's Listen holds, from
// before the socket exists until after it is gone, on the lock file next
// to it. The only way to ask is to take the lock, so a shared one is taken
// and dropped at once; it fails exactly while a daemon holds its exclusive
// one. No lock file, or one that cannot be probed, counts as free: there
// is nothing to wait for.
func lockReleased(ep Endpoint) bool {
	f, err := os.Open(protocol.LockPath(ep.Address)) // #nosec G304 -- path derived from the socket path
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	fd := int(f.Fd()) // #nosec G115 -- a file descriptor fits an int
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return !errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	return true
}
