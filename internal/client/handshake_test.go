package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"somad/internal/protocol"
	"somad/internal/tlsutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here cover a client and a daemon that reach each other but were
// paired wrongly: every such failure must name its cause, and none may read
// as "no daemon there" (the "cannot reach ... run `soma daemon` there"
// lead-in is for a dial that failed).

// startFakeTLSServer is startFakeTCPServer serving TLS with a freshly
// generated certificate. It returns the address and the certificate's path.
func startFakeTLSServer(t *testing.T, handle func(req protocol.Request, send func(v any))) (addr, certPath string) {
	t.Helper()
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	_, err := tlsutil.EnsureServerCert(certPath, keyPath, []string{"127.0.0.1"})
	require.NoError(t, err)
	serverCfg, _, err := tlsutil.ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	fs := &fakeServer{t: t, ln: tls.NewListener(ln, serverCfg), handle: handle}
	go fs.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), certPath
}

// startPlaintextDaemon listens on a loopback TCP port like a daemon serving
// plaintext: whatever line arrives that is not a request (a TLS hello, say)
// is answered with a JSON error when answer is set, as the real daemon
// does, and with silence otherwise, as it does while no newline has come.
func startPlaintextDaemon(t *testing.T, answer bool) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
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
				buf := make([]byte, 4096)
				for {
					if _, err := nc.Read(buf); err != nil {
						return
					}
					if answer {
						_ = protocol.WriteLine(nc, protocol.Response{Error: "malformed request: invalid character"})
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// pskHandler answers the pre-shared-key exchange like a daemon whose key is
// psk, and everything else like defaultHandler.
func pskHandler(psk string) func(req protocol.Request, send func(v any)) {
	fallback := defaultHandler("dev")
	// One connection at a time is all these tests make, so the outstanding
	// nonce needs no per-connection bookkeeping.
	var nonce []byte
	return func(req protocol.Request, send func(v any)) {
		switch req.Method {
		case protocol.MethodAuthChallenge:
			nonce, _ = protocol.NewAuthNonce()
			raw, _ := json.Marshal(protocol.AuthChallengeResult{Nonce: base64.StdEncoding.EncodeToString(nonce)})
			send(protocol.Response{ID: req.ID, Result: raw})
		case protocol.MethodAuth:
			var p protocol.AuthParams
			_ = json.Unmarshal(req.Params, &p)
			mac, _ := base64.StdEncoding.DecodeString(p.MAC)
			if !protocol.VerifyAuthMAC(psk, nonce, mac) {
				send(protocol.Response{ID: req.ID, Error: "authentication failed: pre-shared key mismatch"})
				return
			}
			send(protocol.Response{ID: req.ID, Result: json.RawMessage(`{}`)})
		default:
			fallback(req, send)
		}
	}
}

// requireHandshakeError asserts that err is the failure of a daemon that
// answered, never the lead-in for one that cannot be reached, and returns
// it.
func requireHandshakeError(t *testing.T, err error) *HandshakeError {
	t.Helper()
	var he *HandshakeError
	require.ErrorAs(t, err, &he)
	assert.NotContains(t, err.Error(), "cannot reach")
	assert.NotContains(t, err.Error(), "run `soma daemon` there")
	return he
}

func TestEnsureServer_PlaintextClientOfATLSDaemonIsToldSo(t *testing.T) {
	addr, _ := startFakeTLSServer(t, defaultHandler("dev"))

	// Without a pre-shared key the first round trip is hello.
	_, _, err := EnsureServer(Endpoint{Network: "tcp", Address: addr}, "dev")
	he := requireHandshakeError(t, err)
	assert.Equal(t, hintDaemonWantsTLS, he.Hint)
	assert.Contains(t, err.Error(), "handshake with soma daemon failed")
	assert.Contains(t, err.Error(), "may require TLS")
	assert.Contains(t, err.Error(), "--tls-fingerprint")

	// With one it is the authentication, which DialEndpoint performs.
	_, _, err = EnsureServer(Endpoint{Network: "tcp", Address: addr, PSK: "sesame"}, "dev")
	he = requireHandshakeError(t, err)
	assert.Equal(t, hintDaemonWantsTLS, he.Hint)
	assert.Contains(t, err.Error(), "authenticating with tcp://"+addr)
}

func TestEnsureServer_TLSClientOfAPlaintextDaemonIsToldSo(t *testing.T) {
	prev := dialTimeout
	dialTimeout = 300 * time.Millisecond
	t.Cleanup(func() { dialTimeout = prev })

	clientCfg, err := tlsutil.ClientTLSConfig("", "", "127.0.0.1")
	require.NoError(t, err)

	for name, answer := range map[string]bool{
		"the daemon answers the TLS hello with JSON": true,
		"the daemon waits for the rest of a line":    false,
	} {
		t.Run(name, func(t *testing.T) {
			addr := startPlaintextDaemon(t, answer)

			_, _, err := EnsureServer(Endpoint{Network: "tcp", Address: addr, TLS: clientCfg}, "dev")

			he := requireHandshakeError(t, err)
			assert.Equal(t, hintDaemonLacksTLS, he.Hint)
			assert.Contains(t, err.Error(), "TLS handshake with tcp://"+addr+" failed")
			assert.Contains(t, err.Error(), "may not be using TLS")
		})
	}
}

func TestEnsureServer_WrongPSKIsNotAnUnreachableDaemon(t *testing.T) {
	addr := startFakeTCPServer(t, pskHandler("sesame"))

	_, _, err := EnsureServer(Endpoint{Network: "tcp", Address: addr, PSK: "wrong"}, "dev")

	he := requireHandshakeError(t, err)
	assert.Empty(t, he.Hint, "the daemon's own answer names the cause")
	assert.Contains(t, err.Error(), "authenticating with tcp://"+addr)
	assert.Contains(t, err.Error(), "pre-shared key mismatch")

	// The right key still gets through.
	c, _, err := EnsureServer(Endpoint{Network: "tcp", Address: addr, PSK: "sesame"}, "dev")
	require.NoError(t, err)
	_ = c.Close()
}

func TestEnsureServer_PinnedCertificateMismatchIsNotAnUnreachableDaemon(t *testing.T) {
	addr, _ := startFakeTLSServer(t, defaultHandler("dev"))
	// The fingerprint of some other certificate.
	clientCfg, err := tlsutil.ClientTLSConfig("", "sha256:"+
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "127.0.0.1")
	require.NoError(t, err)

	_, _, err = EnsureServer(Endpoint{Network: "tcp", Address: addr, TLS: clientCfg}, "dev")

	he := requireHandshakeError(t, err)
	assert.Empty(t, he.Hint, "the mismatch names the cause")
	assert.Contains(t, err.Error(), "fingerprint mismatch")
}

func TestDialEndpoint_OnlyAnAnsweringDaemonIsAHandshakeError(t *testing.T) {
	_, err := DialEndpoint(Endpoint{Network: "tcp", Address: unreachableTCPAddr(t)})
	require.Error(t, err)
	var he *HandshakeError
	assert.NotErrorAs(t, err, &he, "nothing answered: that is a dial failure")

	// Another user's daemon on the local socket did answer.
	path := testSocketPath(t)
	startFakeServer(t, path, defaultHandler("dev"))
	fakePeerUID(t, foreignUID(), nil)
	_, err = Dial(path)
	require.ErrorIs(t, err, ErrForeignDaemon)
	assert.ErrorAs(t, err, &he)
}

func TestHandshake_LocalConnectionLossCarriesNoTLSHint(t *testing.T) {
	path := testSocketPath(t)
	startFakeServer(t, path, defaultHandler("dev"))
	c, err := Dial(path)
	require.NoError(t, err)
	// The connection goes away before hello, as with a daemon shutting down.
	require.NoError(t, c.nc.Close())

	_, err = Handshake(c, UnixEndpoint(path), "dev")

	var he *HandshakeError
	require.ErrorAs(t, err, &he)
	assert.Empty(t, he.Hint, "the Unix socket never serves TLS")
	assert.ErrorIs(t, err, ErrDisconnected)
}
