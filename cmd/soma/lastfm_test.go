package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"somad/internal/client"
	"somad/internal/config"
	"somad/internal/lastfm"
	"somad/internal/protocol"
	"somad/internal/security/securitytest"
	"somad/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestConfig writes soma's config.yaml where config.Load will find it,
// via the XDG override, and returns it loaded the way main does.
func writeTestConfig(t *testing.T, content string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := config.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
}

// startFakeLastfm serves auth.getToken and auth.getSession, standing in for
// Last.fm during the desktop auth flow, and points lastfm.APIURL at it.
func startFakeLastfm(t *testing.T, token, sessionKey string) {
	t.Helper()
	securitytest.AllowTestHosts(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		switch r.FormValue("method") {
		case "auth.getToken":
			_, _ = w.Write([]byte(`{"token":"` + token + `"}`))
		case "auth.getSession":
			_, _ = w.Write([]byte(`{"session":{"name":"someuser","key":"` + sessionKey + `","subscriber":0}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	prev := lastfm.APIURL
	lastfm.APIURL = srv.URL + "/"
	t.Cleanup(func() { lastfm.APIURL = prev })
}

// stubOpenURL replaces openURLFunc with a no-op for the duration of t, so
// tests never launch a real browser.
func stubOpenURL(t *testing.T) {
	t.Helper()
	prev := openURLFunc
	openURLFunc = func(string) error { return nil }
	t.Cleanup(func() { openURLFunc = prev })
}

// stubLoginInput feeds a scripted "Enter" keypress to runLastfmLogin instead
// of blocking on a real terminal.
func stubLoginInput(t *testing.T) {
	t.Helper()
	prev := lastfmLoginInput
	lastfmLoginInput = strings.NewReader("\n")
	t.Cleanup(func() { lastfmLoginInput = prev })
}

func TestRunLastfmLogin_Success(t *testing.T) {
	cfg := writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	startFakeLastfm(t, "tok123", "sess456")
	stubOpenURL(t)
	stubLoginInput(t)
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runLastfmLogin(cfg, nil) })

	assert.Contains(t, out, "https://www.last.fm/api/auth/")
	assert.Contains(t, out, "tok123")
	assert.Contains(t, out, "Logged in to last.fm.")

	key, err := state.LoadLastfmSession()
	require.NoError(t, err)
	assert.Equal(t, "sess456", key)

	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.lastfmReloads, "a successful login must tell the running daemon to reload")
}

func TestRunLastfmLogin_WorksWithoutARunningDaemon(t *testing.T) {
	cfg := writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	startFakeLastfm(t, "tok123", "sess456")
	stubOpenURL(t)
	stubLoginInput(t)
	// No fakeDaemon: point at a socket nothing is listening on.
	setEndpoint(t, client.UnixEndpoint(filepath.Join(shortTempDir(t), "absent.sock")))

	out := captureStdout(t, func() { runLastfmLogin(cfg, nil) })

	assert.Contains(t, out, "Logged in to last.fm.")
	key, err := state.LoadLastfmSession()
	require.NoError(t, err)
	assert.Equal(t, "sess456", key)
}

func TestRunLastfmLogout_RemovesSessionAndReloadsDaemon(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	require.NoError(t, state.SaveLastfmSession("sess456"))
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runLastfmLogout(nil) })

	assert.Contains(t, out, "Logged out of last.fm.")
	key, err := state.LoadLastfmSession()
	require.NoError(t, err)
	assert.Empty(t, key)

	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.lastfmReloads)
}

// captureLastfmOutput runs fn with os.Stdout and os.Stderr redirected and
// returns what it wrote to each.
func captureLastfmOutput(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()
	stdout = captureStdout(t, fn)
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return stdout, string(out)
}

// startReloadFailingDaemon serves just enough of the protocol for
// reloadRunningDaemon: hello succeeds and reloadLastfm fails, as a daemon
// whose config file no longer loads answers.
func startReloadFailingDaemon(t *testing.T) {
	t.Helper()
	path := filepath.Join(shortTempDir(t), "d.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = nc.Close() }()
				sc := protocol.NewScanner(nc)
				for sc.Scan() {
					var req protocol.Request
					if json.Unmarshal(sc.Bytes(), &req) != nil {
						continue
					}
					resp := protocol.Response{ID: req.ID, Error: "error loading config: bad yaml"}
					if req.Method == protocol.MethodHello {
						resp.Error = ""
						resp.Result, _ = json.Marshal(protocol.HelloResult{ServerVersion: version, ProtocolVersion: protocol.Version})
					}
					_ = protocol.WriteLine(nc, resp)
				}
			}()
		}
	}()
	setEndpoint(t, client.UnixEndpoint(path))
}

func TestRunLastfmLogout_ReloadFailureGoesToStderr(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	startReloadFailingDaemon(t)

	stdout, stderr := captureLastfmOutput(t, func() { runLastfmLogout(nil) })

	assert.Contains(t, stdout, "Logged out of last.fm.")
	assert.NotContains(t, stdout, "could not tell the running daemon")
	assert.Contains(t, stderr, "could not tell the running daemon to reload: error loading config: bad yaml")
}

func TestRunLastfmLogout_RemoteDaemonGetsANoteNotAReload(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	d := startFakeDaemon(t)
	// The same fake, reached over TCP the way a remote daemon is.
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(nc)
		}
	}()
	setEndpoint(t, client.Endpoint{Network: "tcp", Address: ln.Addr().String()})

	stdout, stderr := captureLastfmOutput(t, func() { runLastfmLogout(nil) })

	assert.Contains(t, stdout, "Logged out of last.fm.")
	assert.Contains(t, stderr, "tcp://"+ln.Addr().String()+" reads its own")
	assert.Contains(t, stderr, `run "soma lastfm logout" on that host`)
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Zero(t, d.lastfmReloads, "the remote daemon's session is not the one that changed")
}

// TestSetUpLastfm_ReloadSeesConfigChangedAfterStartup covers a daemon
// started before lastfm.api_key/api_secret were set: the reloadLastfm hooks
// re-read the config file, so "soma lastfm login" can still start
// scrobbling without a restart.
func TestSetUpLastfm_ReloadSeesConfigChangedAfterStartup(t *testing.T) {
	cfg := writeTestConfig(t, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	scrobbler, resolveSession, loadScrobbler := setUpLastfm(cfg)
	assert.True(t, scrobbler == nil, "no scrobbler without credentials")
	loaded, err := loadScrobbler()
	require.NoError(t, err)
	assert.True(t, loaded == nil, "still no credentials: an untyped nil, not a nil *lastfm.Client")

	path, err := config.Path()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("lastfm:\n  api_key: testkey\n  api_secret: testsecret\n"), 0o600))
	require.NoError(t, state.SaveLastfmSession("sess456"))

	loaded, err = loadScrobbler()
	require.NoError(t, err)
	assert.NotNil(t, loaded)
	key, err := resolveSession()
	require.NoError(t, err)
	assert.Equal(t, "sess456", key)

	require.NoError(t, os.WriteFile(path, []byte("lastfm:\n  bogus: 1\n"), 0o600))
	_, err = loadScrobbler()
	require.ErrorContains(t, err, "error loading config")
	_, err = resolveSession()
	require.ErrorContains(t, err, "error loading config")
}

func TestRunLastfmStatus_NotConfigured(t *testing.T) {
	cfg := writeTestConfig(t, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out := captureStdout(t, func() { runLastfmStatus(cfg, nil) })
	assert.Contains(t, out, "not configured")
}

func TestRunLastfmStatus_ConfiguredNotLoggedIn(t *testing.T) {
	cfg := writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out := captureStdout(t, func() { runLastfmStatus(cfg, nil) })
	assert.Contains(t, out, "configured, not logged in")
}

func TestRunLastfmStatus_ConfiguredAndLoggedIn(t *testing.T) {
	cfg := writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	require.NoError(t, state.SaveLastfmSession("sess456"))

	out := captureStdout(t, func() { runLastfmStatus(cfg, nil) })
	assert.Contains(t, out, "configured and logged in")
}

func TestRunLastfmStatus_JSON(t *testing.T) {
	cfg := writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	require.NoError(t, state.SaveLastfmSession("sess456"))

	out := captureStdout(t, func() { runLastfmStatus(cfg, []string{"--json"}) })

	var got lastfmStatusResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.True(t, got.Configured)
	assert.True(t, got.LoggedIn)
}

func TestRunLastfmStatus_SessionKeyConfigOverrideCountsAsLoggedIn(t *testing.T) {
	cfg := writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n  session_key: overridden\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// No state file at all: the config override alone must be enough.

	out := captureStdout(t, func() { runLastfmStatus(cfg, nil) })
	assert.Contains(t, out, "configured and logged in")
}

func TestRunLastfm_DispatchesToSubcommands(t *testing.T) {
	cfg := writeTestConfig(t, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out := captureStdout(t, func() { runLastfm(cfg, []string{"status"}) })
	assert.Contains(t, out, "not configured")
}
