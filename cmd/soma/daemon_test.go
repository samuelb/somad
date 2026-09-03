package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"somad/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePSK_ReturnsDistinctHexKeys(t *testing.T) {
	a, err := generatePSK()
	require.NoError(t, err)
	b, err := generatePSK()
	require.NoError(t, err)

	// pskBytes bytes, hex-encoded, is twice as many characters.
	assert.Len(t, a, pskBytes*2)
	assert.NotEqual(t, a, b, "two generated keys should not collide")
	// readPSKFile must accept it unchanged: no whitespace to trim beyond
	// the trailing newline writeGeneratedPSK adds.
	assert.NotContains(t, a, " ")
}

func TestWriteGeneratedPSK_WritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")

	require.NoError(t, writeGeneratedPSK(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// The written file must itself pass the permission check and round-trip
	// through readPSKFile as a single trimmed line.
	psk, err := readPSKFile(path)
	require.NoError(t, err)
	assert.Len(t, psk, pskBytes*2)
}

func TestWriteGeneratedPSK_RefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o600))

	err := writeGeneratedPSK(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	data, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(data), "the existing file must be left untouched")
}

func TestResolveDaemonOptions_ConfigDefaultsAndFlagOverrides(t *testing.T) {
	trayOff := false
	notifyOn := true
	quality := "low"
	listen := ":5454"
	idle := config.Duration(5 * time.Minute)
	cfg := &config.Config{Server: config.ServerConfig{
		IdleTimeout: &idle,
		Tray:        &trayOff, // Tray:false means --no-tray defaults to true.
		Notify:      &notifyOn,
		Quality:     &quality,
		Listen:      &listen,
	}}

	tests := []struct {
		name string
		args []string
		want daemonOptions
	}{
		{
			name: "no flags: config supplies every default",
			want: daemonOptions{
				action:      daemonActionRun,
				idleTimeout: 5 * time.Minute,
				noTray:      true,
				notify:      true,
				quality:     "low",
				listen:      ":5454",
			},
		},
		{
			name: "explicit flags win over config",
			args: []string{
				"--idle-timeout=1h",
				"--no-tray=false",
				"--notify=false",
				"--quality=high",
				"--listen=:9999",
			},
			want: daemonOptions{
				action:      daemonActionRun,
				idleTimeout: time.Hour,
				noTray:      false,
				notify:      false,
				quality:     "high",
				listen:      ":9999",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDaemonOptions(cfg, tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.want.action, got.action)
			assert.Equal(t, tt.want.idleTimeout, got.idleTimeout)
			assert.Equal(t, tt.want.noTray, got.noTray)
			assert.Equal(t, tt.want.notify, got.notify)
			assert.Equal(t, tt.want.quality, got.quality)
			assert.Equal(t, tt.want.listen, got.listen)
		})
	}
}

func TestResolveDaemonOptions_ExpandsHomeInFlagPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config/somad"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config/somad/psk"), []byte("secret\n"), 0o600))

	opts, err := resolveDaemonOptions(&config.Config{}, []string{"--psk-file=~/.config/somad/psk"})

	require.NoError(t, err)
	assert.Equal(t, "secret", opts.psk, "a quoted ~/-prefixed --psk-file must expand like the shell would")
}

func TestResolveDaemonOptions_RejectsInvalidQuality(t *testing.T) {
	_, err := resolveDaemonOptions(&config.Config{}, []string{"--quality=ultra"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--quality")
}

func TestResolveDaemonOptions_RequiresTLSCertAndKeyTogether(t *testing.T) {
	for name, args := range map[string][]string{
		"cert without key": {"--tls-cert=/tmp/cert.pem"},
		"key without cert": {"--tls-key=/tmp/key.pem"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveDaemonOptions(&config.Config{}, args)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "must be set together")
		})
	}
}

func TestResolveDaemonOptions_PSKFileTakesPrecedenceOverConfigPSK(t *testing.T) {
	pskPath := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(pskPath, []byte("from-file\n"), 0o600))

	configPSK := "from-config"
	cfg := &config.Config{Server: config.ServerConfig{PSK: &configPSK}}

	opts, err := resolveDaemonOptions(cfg, nil)
	require.NoError(t, err)
	assert.Equal(t, "from-config", opts.psk, "with no --psk-file, server.psk applies")

	opts, err = resolveDaemonOptions(cfg, []string{"--psk-file=" + pskPath})
	require.NoError(t, err)
	assert.Equal(t, "from-file", opts.psk, "--psk-file wins even though server.psk is also set")
}

func TestResolveDaemonOptions_GenPSKAction(t *testing.T) {
	t.Run("explicit --psk-file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "psk")

		opts, err := resolveDaemonOptions(&config.Config{}, []string{"--gen-psk", "--psk-file=" + path})

		require.NoError(t, err)
		assert.Equal(t, daemonActionGenPSK, opts.action)
		assert.Equal(t, path, opts.genPSKPath)
	})

	t.Run("default path under the config directory", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())

		opts, err := resolveDaemonOptions(&config.Config{}, []string{"--gen-psk"})

		require.NoError(t, err)
		assert.Equal(t, daemonActionGenPSK, opts.action)
		assert.Equal(t, "psk", filepath.Base(opts.genPSKPath))
	})
}

func TestResolveDaemonOptions_ShowCertAction(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	opts, err := resolveDaemonOptions(&config.Config{}, []string{"--show-cert"})

	require.NoError(t, err)
	assert.Equal(t, daemonActionShowCert, opts.action)
	assert.NotEmpty(t, opts.certPath)
	assert.Contains(t, opts.certFingerprint, "sha256:")
}
