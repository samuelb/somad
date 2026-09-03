package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"somad/internal/client"
	"somad/internal/config"
	"somad/internal/lastfm"
	"somad/internal/security/securitytest"
	"somad/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTestConfig writes soma's config.yaml where config.Load will find it,
// via the XDG override.
func writeTestConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, err := config.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
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
	writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	startFakeLastfm(t, "tok123", "sess456")
	stubOpenURL(t)
	stubLoginInput(t)
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runLastfmLogin(nil) })

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
	writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	startFakeLastfm(t, "tok123", "sess456")
	stubOpenURL(t)
	stubLoginInput(t)
	// No fakeDaemon: point at a socket nothing is listening on.
	setEndpoint(t, client.UnixEndpoint(filepath.Join(shortTempDir(t), "absent.sock")))

	out := captureStdout(t, func() { runLastfmLogin(nil) })

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

func TestRunLastfmStatus_NotConfigured(t *testing.T) {
	writeTestConfig(t, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out := captureStdout(t, func() { runLastfmStatus(nil) })
	assert.Contains(t, out, "not configured")
}

func TestRunLastfmStatus_ConfiguredNotLoggedIn(t *testing.T) {
	writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out := captureStdout(t, func() { runLastfmStatus(nil) })
	assert.Contains(t, out, "configured, not logged in")
}

func TestRunLastfmStatus_ConfiguredAndLoggedIn(t *testing.T) {
	writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	require.NoError(t, state.SaveLastfmSession("sess456"))

	out := captureStdout(t, func() { runLastfmStatus(nil) })
	assert.Contains(t, out, "configured and logged in")
}

func TestRunLastfmStatus_JSON(t *testing.T) {
	writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	require.NoError(t, state.SaveLastfmSession("sess456"))

	out := captureStdout(t, func() { runLastfmStatus([]string{"--json"}) })

	var got lastfmStatusResult
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.True(t, got.Configured)
	assert.True(t, got.LoggedIn)
}

func TestRunLastfmStatus_SessionKeyConfigOverrideCountsAsLoggedIn(t *testing.T) {
	writeTestConfig(t, "lastfm:\n  api_key: testkey\n  api_secret: testsecret\n  session_key: overridden\n")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// No state file at all: the config override alone must be enough.

	out := captureStdout(t, func() { runLastfmStatus(nil) })
	assert.Contains(t, out, "configured and logged in")
}

func TestRunLastfm_DispatchesToSubcommands(t *testing.T) {
	writeTestConfig(t, "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out := captureStdout(t, func() { runLastfm([]string{"status"}) })
	assert.Contains(t, out, "not configured")
}
