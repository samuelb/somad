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

func TestResolveEndpoint_PSKFileFlagExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config/somad"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config/somad/psk"), []byte("secret\n"), 0o600))

	ep, err := resolveEndpoint(connFlags{
		server:  "myserver:5454",
		pskFile: "~/.config/somad/psk",
	}, &config.Config{})

	require.NoError(t, err)
	assert.Equal(t, "secret", ep.PSK, "a quoted ~/-prefixed --psk-file must expand like the shell would")
}

func TestResolveEndpoint_TLSCAFlagExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	caPath := filepath.Join(home, "ca.pem")
	// tlsutil.ClientTLSConfig validates the CA file's contents, so this only
	// needs to prove the path itself was expanded before being read; a
	// malformed PEM file surfaces as an error naming the expanded path.
	require.NoError(t, os.WriteFile(caPath, []byte("not a real certificate"), 0o600))

	_, err := resolveEndpoint(connFlags{
		server: "myserver:5454",
		tlsCA:  "~/ca.pem",
	}, &config.Config{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), caPath, "the error must name the expanded path")
	assert.NotContains(t, err.Error(), "~/ca.pem", "the ~ must have been expanded before the file was opened")
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

// openPSKFile opens path for the checkPSKFilePermissions tests, which check
// an already-open file the same way readPSKFile does.
func openPSKFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- test-controlled path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestCheckPSKFilePermissions_RejectsGroupAndWorldReadable(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "psk")
			require.NoError(t, os.WriteFile(path, []byte("secret\n"), mode)) // #nosec G306 -- intentionally permissive for the rejection test

			err := checkPSKFilePermissions(openPSKFile(t, path))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "must not be accessible")
		})
	}
}

func TestCheckPSKFilePermissions_AcceptsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(path, []byte("secret\n"), 0o600))

	assert.NoError(t, checkPSKFilePermissions(openPSKFile(t, path)))
}

func TestCheckPSKFilePermissions_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()

	f, err := os.Open(dir) // #nosec G304 -- test-controlled path
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	err = checkPSKFilePermissions(f)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestReadPSKFile_RejectsGroupReadable(t *testing.T) {
	pskPath := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(pskPath, []byte("secret\n"), 0o640)) // #nosec G306 -- intentionally permissive for the rejection test

	_, err := readPSKFile(pskPath)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be accessible")
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
