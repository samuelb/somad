package tlsutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
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

// Two daemons started at once get here before either holds the
// single-instance lock, and on the first start with TLS both find no pair.
// Each used to write its own key and certificate, which left the key of one
// with the certificate of the other, or replaced the pair a daemon had
// already loaded.
func TestEnsureServerCert_ConcurrentCallsEndUpWithOnePair(t *testing.T) {
	const callers = 8
	type result struct {
		created     bool
		fingerprint string
		err         error
	}
	for round := range 10 {
		dir := t.TempDir()
		certPath := filepath.Join(dir, "cert.pem")
		keyPath := filepath.Join(dir, "key.pem")

		start := make(chan struct{})
		results := make(chan result, callers)
		for range callers {
			go func() {
				<-start
				var r result
				if r.created, r.err = EnsureServerCert(certPath, keyPath, nil); r.err == nil {
					_, r.fingerprint, r.err = ServerTLSConfig(certPath, keyPath)
				}
				results <- r
			}()
		}
		close(start)

		created := 0
		fingerprints := make(map[string]bool)
		for range callers {
			r := <-results
			require.NoError(t, r.err, "round %d: every caller must get a pair that loads", round)
			if r.created {
				created++
			}
			fingerprints[r.fingerprint] = true
		}
		assert.Equal(t, 1, created, "round %d: one caller generates, the others take what it wrote", round)
		assert.Len(t, fingerprints, 1, "round %d: every caller must serve the same certificate", round)
		_, left, err := ServerTLSConfig(certPath, keyPath)
		require.NoError(t, err, "round %d: the pair left on disk must load", round)
		assert.True(t, fingerprints[left], "round %d: and be the one the callers loaded", round)
	}
}

// A key and a certificate that do not belong together cannot be served, so
// no client can be paired with them, and left alone they keep the daemon
// from starting at all until someone deletes them by hand.
func TestEnsureServerCert_ReplacesAPairThatDoesNotLoad(t *testing.T) {
	certPath, keyPath, _ := genPair(t)
	_, otherKey, _ := genPair(t)
	stray, err := os.ReadFile(otherKey) // #nosec G304 -- test path under t.TempDir
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(keyPath, stray, 0o600)) // #nosec G703 -- test path under t.TempDir
	oldCert, err := os.ReadFile(certPath)                   // #nosec G304 -- test path under t.TempDir
	require.NoError(t, err)
	_, _, err = ServerTLSConfig(certPath, keyPath)
	require.Error(t, err, "the mismatched pair must not load to begin with")

	created, err := EnsureServerCert(certPath, keyPath, nil)
	require.NoError(t, err)
	assert.True(t, created)
	_, _, err = ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	// Both halves are kept, as for any corrupt file the daemon wrote.
	kept, err := os.ReadFile(certPath + ".corrupt") // #nosec G304 -- test path under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, oldCert, kept)
	info, err := os.Stat(keyPath + ".corrupt")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the key moved aside stays private")
}

// A state directory the daemon cannot write to has no room for the lock
// file. With a pair in place there is nothing to generate, and nothing to
// lock for.
func TestEnsureServerCert_ReadOnlyDirectoryWithAPairStillWorks(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}
	certPath, keyPath, fingerprint := genPair(t)
	dir := filepath.Dir(certPath)
	require.NoError(t, os.Remove(certPath+".lock"))
	require.NoError(t, os.Chmod(dir, 0o500))       // #nosec G302 -- a directory, for this test read-only
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // #nosec G302 -- restores the temp dir so it can be removed

	created, err := EnsureServerCert(certPath, keyPath, nil)
	require.NoError(t, err)
	assert.False(t, created)
	_, again, err := ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)
	assert.Equal(t, fingerprint, again)
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

func TestExplainHostnameMismatch_ListsCertificateNames(t *testing.T) {
	certPath, keyPath, _ := genPair(t)
	serverCfg, _, err := ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	// The usual LAN setup: the CA file is right, but the client dials the
	// server by an address the auto-generated certificate does not name.
	clientCfg, err := ClientTLSConfig(certPath, "", "192.168.1.20")
	require.NoError(t, err)
	err = ExplainHostnameMismatch(handshake(t, serverCfg, clientCfg))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "192.168.1.20", "the original error is kept")
	assert.Contains(t, err.Error(), "certificate names localhost")
	assert.Contains(t, err.Error(), "127.0.0.1")
	assert.Contains(t, err.Error(), "--tls-fingerprint")
	var hostErr x509.HostnameError
	assert.ErrorAs(t, err, &hostErr, "the hint wraps rather than replaces the error")
}

func TestExplainHostnameMismatch_LeavesOtherErrorsAlone(t *testing.T) {
	other := errors.New("connection reset")
	assert.Equal(t, other, ExplainHostnameMismatch(other))
	assert.NoError(t, ExplainHostnameMismatch(nil))
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
