package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeConfig places a config file where Load will find it, using the XDG
// override to point at a temp directory.
func writeConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, appDirName), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, appDirName, configFileName), []byte(content), 0o600))
}

func TestPathUsesXDGOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	path, err := Path()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/custom/config", appDirName, configFileName), path)
}

func TestLoadMissingFileIsEmptyConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.Server.IdleTimeout)
	assert.Nil(t, cfg.Server.Tray)
	assert.Nil(t, cfg.TUI.ShutdownOnExit)
}

func TestLoadEmptyFileIsEmptyConfig(t *testing.T) {
	writeConfig(t, "")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.Server.IdleTimeout)
	assert.Nil(t, cfg.Server.Tray)
	assert.Nil(t, cfg.TUI.ShutdownOnExit)
}

func TestLoadFullConfig(t *testing.T) {
	writeConfig(t, "server:\n  idle_timeout: 5m\n  tray: false\ntui:\n  shutdown_on_exit: true\n")
	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg.Server.IdleTimeout)
	assert.Equal(t, 5*time.Minute, time.Duration(*cfg.Server.IdleTimeout))
	require.NotNil(t, cfg.Server.Tray)
	assert.False(t, *cfg.Server.Tray)
	require.NotNil(t, cfg.TUI.ShutdownOnExit)
	assert.True(t, *cfg.TUI.ShutdownOnExit)
}

func TestLoadPartialConfigLeavesRestUnset(t *testing.T) {
	writeConfig(t, "server:\n  tray: true\n")
	cfg, err := Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.Server.IdleTimeout)
	require.NotNil(t, cfg.Server.Tray)
	assert.True(t, *cfg.Server.Tray)
	assert.Nil(t, cfg.TUI.ShutdownOnExit)
}

func TestLoadZeroIdleTimeoutIsExplicit(t *testing.T) {
	writeConfig(t, "server:\n  idle_timeout: \"0\"\n")
	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg.Server.IdleTimeout)
	assert.Equal(t, time.Duration(0), time.Duration(*cfg.Server.IdleTimeout))
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	writeConfig(t, "server: [broken\n")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid config file")
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	writeConfig(t, "server:\n  idle_timout: 5m\n") // typo'd key
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "idle_timout")
}

func TestLoadRejectsBareNumberDuration(t *testing.T) {
	writeConfig(t, "server:\n  idle_timeout: 300\n")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `invalid duration "300"`)
}

func TestLoadRejectsMalformedDuration(t *testing.T) {
	writeConfig(t, "server:\n  idle_timeout: soon\n")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid duration")
}

func TestLoadRejectsNegativeIdleTimeout(t *testing.T) {
	writeConfig(t, "server:\n  idle_timeout: -5m\n")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be negative")
}

func TestEnsureTemplateCreatesParseableDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, created, err := EnsureTemplate(2 * time.Minute)
	require.NoError(t, err)
	assert.True(t, created)
	wantPath, err := Path()
	require.NoError(t, err)
	assert.Equal(t, wantPath, path)

	// Everything in the template is commented out, so loading it must yield
	// the same empty config as having no file at all.
	cfg, err := Load()
	require.NoError(t, err)
	assert.Nil(t, cfg.Server.IdleTimeout)
	assert.Nil(t, cfg.Server.Tray)
	assert.Nil(t, cfg.TUI.ShutdownOnExit)

	// The commented-out settings must be real: uncommenting them (dropping
	// the prose header, which uses "# " with a single space) has to parse
	// and reflect the documented defaults.
	data, err := os.ReadFile(path) // #nosec G304 -- test path under t.TempDir
	require.NoError(t, err)
	var settings []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "#  ") || regexp.MustCompile(`^#[^ ]`).MatchString(line) {
			settings = append(settings, line[1:])
		}
	}
	require.NotEmpty(t, settings)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(settings, "\n")), 0o600)) // #nosec G703 -- test path under t.TempDir
	cfg, err = Load()
	require.NoError(t, err)
	require.NotNil(t, cfg.Server.IdleTimeout)
	assert.Equal(t, 2*time.Minute, time.Duration(*cfg.Server.IdleTimeout))
	require.NotNil(t, cfg.Server.Tray)
	assert.True(t, *cfg.Server.Tray)
	require.NotNil(t, cfg.TUI.ShutdownOnExit)
	assert.False(t, *cfg.TUI.ShutdownOnExit)
}

func TestEnsureTemplateNeverTouchesAnExistingFile(t *testing.T) {
	userContent := "server:\n  tray: false\n"
	writeConfig(t, userContent)

	path, created, err := EnsureTemplate(2 * time.Minute)
	require.NoError(t, err)
	assert.False(t, created)

	data, err := os.ReadFile(path) // #nosec G304 -- test path under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, userContent, string(data))
}

func TestLoadRemoteTransportConfig(t *testing.T) {
	writeConfig(t, `server:
  listen: "0.0.0.0:5454"
  tls: true
  tls_cert: /path/cert.pem
  tls_key: /path/key.pem
  psk: secret
client:
  server: "myserver:5454"
  tls_fingerprint: "sha256:abc"
  psk_file: /path/psk
`)
	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg.Server.Listen)
	assert.Equal(t, "0.0.0.0:5454", *cfg.Server.Listen)
	require.NotNil(t, cfg.Server.TLS)
	assert.True(t, *cfg.Server.TLS)
	require.NotNil(t, cfg.Server.TLSCert)
	require.NotNil(t, cfg.Server.TLSKey)
	require.NotNil(t, cfg.Server.PSK)
	assert.Equal(t, "secret", *cfg.Server.PSK)
	require.NotNil(t, cfg.Client.Server)
	assert.Equal(t, "myserver:5454", *cfg.Client.Server)
	require.NotNil(t, cfg.Client.TLSFingerprint)
	require.NotNil(t, cfg.Client.PSKFile)
}

func TestLoadRejectsContradictoryTransportConfig(t *testing.T) {
	cases := map[string]string{
		"cert without key":        "server:\n  tls_cert: /a.pem\n",
		"key without cert":        "server:\n  tls_key: /a.pem\n",
		"server psk and psk_file": "server:\n  psk: a\n  psk_file: /b\n",
		"client psk and psk_file": "client:\n  psk: a\n  psk_file: /b\n",
		"client tls_ca and pin":   "client:\n  tls_ca: /a.pem\n  tls_fingerprint: \"sha256:x\"\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			writeConfig(t, content)
			_, err := Load()
			assert.Error(t, err)
		})
	}
}
