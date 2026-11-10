package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"somad/internal/client"
	"somad/internal/tlsutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// daemonProcess is a real `soma daemon`, run from the test binary (see
// TestMain) with its config, state, cache and socket in a directory of its
// own, and no tray. It opens no audio device: it never plays, or plays
// through a lostOncePlayer.
type daemonProcess struct {
	ep   client.Endpoint
	cmd  *exec.Cmd
	out  *lockedBuffer
	done chan struct{}
}

// lockedBuffer collects a process's output while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startDaemonProcess starts the daemon with args and waits until its socket
// answers.
func startDaemonProcess(t *testing.T, dir string, args ...string) *daemonProcess {
	t.Helper()
	return startDaemonProcessEnv(t, dir, nil, args...)
}

// startDaemonProcessEnv is startDaemonProcess with env added to the
// daemon's environment.
func startDaemonProcessEnv(t *testing.T, dir string, env []string, args ...string) *daemonProcess {
	t.Helper()
	d := spawnDaemonProcessEnv(t, dir, env, args...)
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := client.DialEndpoint(d.ep)
		if err == nil {
			_ = c.Close()
			return d
		}
		select {
		case <-d.done:
			t.Fatalf("the daemon exited before its socket came up:\n%s", d.out)
		default:
		}
		require.True(t, time.Now().Before(deadline), "the daemon's socket did not come up:\n%s", d.out)
		time.Sleep(20 * time.Millisecond)
	}
}

// spawnDaemonProcess starts the daemon with args without waiting for it.
func spawnDaemonProcess(t *testing.T, dir string, args ...string) *daemonProcess {
	t.Helper()
	return spawnDaemonProcessEnv(t, dir, nil, args...)
}

// spawnDaemonProcessEnv is spawnDaemonProcess with env added to the
// daemon's environment.
func spawnDaemonProcessEnv(t *testing.T, dir string, env []string, args ...string) *daemonProcess {
	t.Helper()
	d := &daemonProcess{
		ep:   client.UnixEndpoint(dir + "/s.sock"),
		out:  &lockedBuffer{},
		done: make(chan struct{}),
	}
	d.cmd = exec.CommandContext(t.Context(), os.Args[0]) // #nosec G204 G702 -- the test binary itself
	d.cmd.Env = append(os.Environ(),
		mainArgsEnv+"="+strings.Join(append([]string{"daemon", "--no-tray"}, args...), "\x1f"),
		"XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir, "XDG_CACHE_HOME="+dir,
		"SOMAD_SOCKET="+d.ep.Address, "SOMAD_SERVER=",
	)
	d.cmd.Env = append(d.cmd.Env, env...)
	d.cmd.Stdout, d.cmd.Stderr = d.out, d.out
	require.NoError(t, d.cmd.Start())
	go func() {
		_ = d.cmd.Wait()
		close(d.done)
	}()
	t.Cleanup(func() {
		select {
		case <-d.done:
		default:
			_ = d.cmd.Process.Kill()
			<-d.done
		}
	})
	return d
}

// exitStatus waits for the daemon to exit and returns its status.
func (d *daemonProcess) exitStatus(t *testing.T) int {
	t.Helper()
	select {
	case <-d.done:
	case <-time.After(15 * time.Second):
		t.Fatalf("the daemon did not exit:\n%s", d.out)
	}
	return d.cmd.ProcessState.ExitCode()
}

// connect returns a hello-verified client to the daemon.
func (d *daemonProcess) connect(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.DialEndpoint(d.ep)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Hello(version)
	require.NoError(t, err)
	return c
}

// A supervised daemon that a client shuts down for a restart exits with the
// status its service manager restarts it for (ADR-0038); stopped for good,
// and when no service manager runs it, it exits 0.
func TestDaemon_ExitStatusTellsAServiceManagerWhetherToRestartIt(t *testing.T) {
	t.Parallel() // each case is a daemon process in a directory of its own
	for name, tc := range map[string]struct {
		args       []string
		restart    bool
		supervised bool
		want       int
	}{
		"supervised, shut down for a restart": {args: []string{"--supervised"}, restart: true, supervised: true, want: restartExitStatus},
		"supervised, stopped":                 {args: []string{"--supervised"}, supervised: true},
		"on its own, shut down for a restart": {restart: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := startDaemonProcess(t, shortTempDir(t), tc.args...)
			c := d.connect(t)
			assert.Equal(t, tc.supervised, c.ServerInfo().Supervised)

			var err error
			if tc.restart {
				err = c.ShutdownForRestart()
			} else {
				err = c.Shutdown()
			}
			if err != nil && !errors.Is(err, client.ErrDisconnected) {
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, d.exitStatus(t), "%s", d.out)
			_, statErr := os.Stat(d.ep.Address)
			assert.True(t, os.IsNotExist(statErr), "the socket is gone with the daemon")
		})
	}
}

// A second daemon on the same socket leaves the first one serving and says
// why it does not run, since its flags then never take effect.
func TestDaemon_SecondInstanceExitsAndSaysWhy(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	first := startDaemonProcess(t, dir)

	second := spawnDaemonProcess(t, dir, "--idle-timeout", "1h")

	assert.Zero(t, second.exitStatus(t))
	assert.Contains(t, second.out.String(), "already running")
	assert.Contains(t, second.out.String(), "soma daemon stop")
	c := first.connect(t)
	_, err := c.Status()
	require.NoError(t, err, "the first daemon goes on serving")
	require.NoError(t, ignoreDisconnect(c.Shutdown()))
	assert.Zero(t, first.exitStatus(t))
}

// ignoreDisconnect drops the error of a request the daemon answered by
// going away.
func ignoreDisconnect(err error) error {
	if errors.Is(err, client.ErrDisconnected) {
		return nil
	}
	return err
}

// tcpListener matches the daemon's log line for a TLS listener: the address
// it bound and the certificate's fingerprint.
var tcpListener = regexp.MustCompile(`listening on tcp://(\S+) with TLS, certificate fingerprint (\S+)`)

// A daemon serving frontends over TCP with TLS and a pre-shared key serves
// a client that has both, and nothing to one without the key.
func TestDaemon_TCPListenerServesOnlyAnAuthenticatedClient(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	pskFile := filepath.Join(dir, "psk")
	const psk = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	require.NoError(t, os.WriteFile(pskFile, []byte(psk+"\n"), 0o600))

	d := startDaemonProcess(t, dir, "--listen", "127.0.0.1:0", "--tls", "--psk-file", pskFile)
	var m []string
	require.Eventually(t, func() bool {
		m = tcpListener.FindStringSubmatch(d.out.String())
		return m != nil
	}, 10*time.Second, 20*time.Millisecond, "the daemon must say where it listens")
	tlsCfg, err := tlsutil.ClientTLSConfig("", m[2], "")
	require.NoError(t, err)
	ep := client.Endpoint{Network: "tcp", Address: m[1], TLS: tlsCfg, PSK: psk}

	c, err := client.DialEndpoint(ep)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	_, err = c.Hello(version)
	require.NoError(t, err)
	_, err = c.Status()
	require.NoError(t, err, "a client with the certificate pinned and the key is served")

	ep.PSK = ""
	stranger, err := client.DialEndpoint(ep)
	require.NoError(t, err, "TLS alone connects")
	t.Cleanup(func() { _ = stranger.Close() })
	_, err = stranger.Hello(version)
	require.Error(t, err, "but nothing is answered before the key exchange")

	ep.PSK = strings.Repeat("f", len(psk))
	_, err = client.DialEndpoint(ep)
	require.Error(t, err, "and the wrong key is turned away")

	require.NoError(t, ignoreDisconnect(c.Shutdown()))
	assert.Zero(t, d.exitStatus(t))
}

// A listener other machines can reach needs TLS and a key: without them the
// daemon refuses to start, and leaves no socket behind.
func TestDaemon_RefusesAnOpenListenerOtherMachinesCanReach(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)

	d := spawnDaemonProcess(t, dir, "--listen", "0.0.0.0:0")

	assert.NotZero(t, d.exitStatus(t))
	assert.Contains(t, d.out.String(), "refusing to serve")
	_, statErr := os.Stat(d.ep.Address)
	assert.True(t, os.IsNotExist(statErr), "nothing is left listening")
}
