package tlsutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genPair generates a certificate pair in a temp dir and returns the paths
// and the certificate's fingerprint.
func genPair(t *testing.T, hosts ...string) (certPath, keyPath, fingerprint string) {
	t.Helper()
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	created, err := EnsureServerCert(certPath, keyPath, hosts)
	require.NoError(t, err)
	require.True(t, created)
	_, fingerprint, err = ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)
	return certPath, keyPath, fingerprint
}

func TestEnsureServerCert_GeneratesAndReuses(t *testing.T) {
	certPath, keyPath, fingerprint := genPair(t)

	for _, p := range []string{certPath, keyPath} {
		info, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), p)
	}

	// A second call must reuse the pair, not mint a new identity.
	created, err := EnsureServerCert(certPath, keyPath, nil)
	require.NoError(t, err)
	assert.False(t, created)
	_, again, err := ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, again)

	assert.True(t, strings.HasPrefix(fingerprint, "sha256:"))
	assert.Len(t, strings.TrimPrefix(fingerprint, "sha256:"), 64)
}

func TestEnsureServerCert_IncludesHosts(t *testing.T) {
	certPath, _, _ := genPair(t, "music.example", "192.168.1.10")

	pemData, err := os.ReadFile(certPath) // #nosec G304 -- test path under t.TempDir
	require.NoError(t, err)
	block, _ := pem.Decode(pemData)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Contains(t, cert.DNSNames, "music.example")
	assert.Contains(t, cert.DNSNames, "localhost")
	ips := make([]string, len(cert.IPAddresses))
	for i, ip := range cert.IPAddresses {
		ips[i] = ip.String()
	}
	assert.Contains(t, ips, "192.168.1.10")
	assert.Contains(t, ips, "127.0.0.1")
}

// handshake runs one client handshake against a TLS server using the given
// configs and returns the client-side handshake error.
func handshake(t *testing.T, serverCfg, clientCfg *tls.Config) error {
	t.Helper()
	ctx := context.Background()
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = nc.Close() }()
		_ = tls.Server(nc, serverCfg).HandshakeContext(ctx)
	}()

	nc, err := (&net.Dialer{}).DialContext(ctx, "tcp", ln.Addr().String())
	require.NoError(t, err)
	defer func() { _ = nc.Close() }()
	tc := tls.Client(nc, clientCfg)
	hsErr := tc.HandshakeContext(ctx)
	_ = tc.Close()
	<-done
	return hsErr
}

func TestClientTLSConfig_FingerprintPinning(t *testing.T) {
	certPath, keyPath, fingerprint := genPair(t)
	serverCfg, _, err := ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	clientCfg, err := ClientTLSConfig("", fingerprint, "127.0.0.1")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, serverCfg, clientCfg))

	// Colons and uppercase (as fingerprint tools often print) still match.
	decorated := "SHA256:" + strings.ToUpper(strings.TrimPrefix(fingerprint, "sha256:"))
	clientCfg, err = ClientTLSConfig("", decorated, "127.0.0.1")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, serverCfg, clientCfg))
}

func TestClientTLSConfig_FingerprintMismatchFails(t *testing.T) {
	certPath, keyPath, _ := genPair(t)
	serverCfg, _, err := ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	_, _, wrong := genPair(t) // a different certificate's fingerprint
	clientCfg, err := ClientTLSConfig("", wrong, "127.0.0.1")
	require.NoError(t, err)
	err = handshake(t, serverCfg, clientCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fingerprint mismatch")
}

func TestClientTLSConfig_CAFile(t *testing.T) {
	certPath, keyPath, _ := genPair(t)
	serverCfg, _, err := ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	// The self-signed certificate itself acts as the CA.
	clientCfg, err := ClientTLSConfig(certPath, "", "localhost")
	require.NoError(t, err)
	assert.NoError(t, handshake(t, serverCfg, clientCfg))

	// A name the certificate does not cover must fail verification.
	clientCfg, err = ClientTLSConfig(certPath, "", "other.example")
	require.NoError(t, err)
	assert.Error(t, handshake(t, serverCfg, clientCfg))
}

func TestClientTLSConfig_Validation(t *testing.T) {
	_, err := ClientTLSConfig("ca.pem", "sha256:abc", "host")
	assert.Error(t, err, "CA file and fingerprint are mutually exclusive")

	_, err = ClientTLSConfig("", "sha256:nothex", "host")
	assert.Error(t, err, "fingerprints must be 64 hex digits")

	cfg, err := ClientTLSConfig("", "", "host")
	require.NoError(t, err)
	assert.False(t, cfg.InsecureSkipVerify, "system-roots mode keeps standard verification")
	assert.Equal(t, "host", cfg.ServerName)
}
