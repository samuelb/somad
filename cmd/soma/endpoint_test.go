package main

import (
	"os"
	"path/filepath"
	"testing"

	"somad/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveEndpoint_DefaultsToUnixSocket(t *testing.T) {
	t.Setenv("SOMAD_SOCKET", "/tmp/test.sock")
	t.Setenv("SOMAD_SERVER", "")
	ep, err := resolveEndpoint(connFlags{}, &config.Config{})
	require.NoError(t, err)
	assert.True(t, ep.IsLocal())
	assert.Equal(t, "/tmp/test.sock", ep.Address)
}

func TestResolveEndpoint_FlagBeatsEnvBeatsConfig(t *testing.T) {
	t.Setenv("SOMAD_SERVER", "env:2")
	cfgAddr := "cfg:3"
	cfg := &config.Config{Client: config.ClientConfig{Server: &cfgAddr}}

	ep, err := resolveEndpoint(connFlags{server: "flag:1"}, cfg)
	require.NoError(t, err)
	assert.Equal(t, "flag:1", ep.Address)

	ep, err = resolveEndpoint(connFlags{}, cfg)
	require.NoError(t, err)
	assert.Equal(t, "env:2", ep.Address)

	t.Setenv("SOMAD_SERVER", "")
	ep, err = resolveEndpoint(connFlags{}, cfg)
	require.NoError(t, err)
	assert.Equal(t, "cfg:3", ep.Address)
	assert.False(t, ep.IsLocal())
	assert.Nil(t, ep.TLS, "TLS stays off unless configured")
}

func TestResolveEndpoint_RejectsAddressWithoutPort(t *testing.T) {
	_, err := resolveEndpoint(connFlags{server: "myserver"}, &config.Config{})
	assert.Error(t, err)
}

func TestResolveEndpoint_TLSAndPSK(t *testing.T) {
	pskPath := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(pskPath, []byte("secret\n"), 0o600))

	ep, err := resolveEndpoint(connFlags{
		server:         "myserver:5454",
		tlsFingerprint: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		pskFile:        pskPath,
	}, &config.Config{})
	require.NoError(t, err)
	require.NotNil(t, ep.TLS, "a trust flag implies TLS")
	assert.Equal(t, "myserver", ep.TLS.ServerName)
	assert.Equal(t, "secret", ep.PSK, "the PSK file is read and trimmed")
}

func TestResolveEndpoint_FlagTrustOverridesConfigTrust(t *testing.T) {
	ca := "/path/ca.pem"
	cfg := &config.Config{Client: config.ClientConfig{TLSCA: &ca}}
	// The config names a CA file; the one-off fingerprint flag must replace
	// it rather than clash with it.
	ep, err := resolveEndpoint(connFlags{
		server:         "myserver:5454",
		tlsFingerprint: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}, cfg)
	require.NoError(t, err)
	assert.NotNil(t, ep.TLS)
}

func TestReadPSKFile_RejectsEmpty(t *testing.T) {
	pskPath := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(pskPath, []byte(" \n"), 0o600))
	_, err := readPSKFile(pskPath)
	assert.Error(t, err)
}

func TestResolveEndpoint_ConnFlagsWithoutServerAreAnError(t *testing.T) {
	t.Setenv("SOMAD_SERVER", "")
	for name, f := range map[string]connFlags{
		"tls":         {tls: true},
		"fingerprint": {tlsFingerprint: "sha256:ab"},
		"psk-file":    {pskFile: "/some/psk"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolveEndpoint(f, &config.Config{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "--server")
		})
	}
}
