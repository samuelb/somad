package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/tlsutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here pair a client and a remote daemon wrongly. The daemon is
// there, so none of these failures may be taken for "no server": that is
// what makes soma status --json report playback as stopped and exit 0.

// listenLoopback returns a loopback TCP listener, wrapped in TLS with a
// fresh certificate when useTLS is set.
func listenLoopback(t *testing.T, useTLS bool) net.Listener {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	if !useTLS {
		return ln
	}
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	_, err = tlsutil.EnsureServerCert(certPath, keyPath, []string{"127.0.0.1"})
	require.NoError(t, err)
	cfg, _, err := tlsutil.ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)
	return tls.NewListener(ln, cfg)
}

// servePaired answers every connection on ln like a daemon whose pre-shared
// key is psk ("" for none): the key exchange, then hello. A line that is
// not a request (a TLS hello reaching a plaintext listener) gets the JSON
// error the real daemon answers it with.
func servePaired(ln net.Listener, psk string) {
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = nc.Close() }()
			var nonce []byte
			sc := protocol.NewScanner(nc)
			for sc.Scan() {
				var req protocol.Request
				resp := protocol.Response{}
				if json.Unmarshal(sc.Bytes(), &req) != nil {
					resp.Error = "malformed request"
					_ = protocol.WriteLine(nc, resp)
					continue
				}
				resp.ID = req.ID
				switch req.Method {
				case protocol.MethodAuthChallenge:
					nonce, _ = protocol.NewAuthNonce()
					resp.Result, _ = json.Marshal(protocol.AuthChallengeResult{Nonce: base64.StdEncoding.EncodeToString(nonce)})
				case protocol.MethodAuth:
					var p protocol.AuthParams
					_ = json.Unmarshal(req.Params, &p)
					mac, _ := base64.StdEncoding.DecodeString(p.MAC)
					if protocol.VerifyAuthMAC(psk, nonce, mac) {
						resp.Result = json.RawMessage(`{}`)
					} else {
						resp.Error = "authentication failed: pre-shared key mismatch"
					}
				case protocol.MethodHello:
					resp.Result, _ = json.Marshal(protocol.HelloResult{ServerVersion: version, ProtocolVersion: protocol.Version})
				case protocol.MethodStatus:
					resp.Result, _ = json.Marshal(protocol.PlaybackState{Status: protocol.StatusPlaying, Volume: 1})
				default:
					resp.Error = "unknown method"
				}
				_ = protocol.WriteLine(nc, resp)
			}
		}()
	}
}

// otherFingerprint is the fingerprint of no certificate these tests make.
const otherFingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestTryDialServer_PairingMistakeIsNotAnAbsentServer(t *testing.T) {
	pinned, err := tlsutil.ClientTLSConfig("", otherFingerprint, "127.0.0.1")
	require.NoError(t, err)

	tests := []struct {
		name      string
		serverTLS bool
		serverPSK string
		clientTLS *tls.Config
		clientPSK string
		want      []string
	}{
		{
			name: "plaintext client, TLS daemon", serverTLS: true,
			want: []string{"handshake with soma daemon failed", "the daemon may require TLS", "--tls-fingerprint"},
		},
		{
			name: "plaintext client with a key, TLS daemon", serverTLS: true, serverPSK: "sesame", clientPSK: "sesame",
			want: []string{"authenticating with tcp://", "the daemon may require TLS", "--tls-fingerprint"},
		},
		{
			name: "TLS client, plaintext daemon", clientTLS: pinned,
			want: []string{"TLS handshake with tcp://", "the daemon may not be using TLS"},
		},
		{
			name: "wrong key", serverPSK: "sesame", clientPSK: "wrong",
			want: []string{"authenticating with tcp://", "pre-shared key mismatch"},
		},
		{
			name: "another certificate than the pinned one", serverTLS: true, clientTLS: pinned,
			want: []string{"TLS handshake with tcp://", "fingerprint mismatch"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln := listenLoopback(t, tt.serverTLS)
			go servePaired(ln, tt.serverPSK)
			setEndpoint(t, client.Endpoint{Network: "tcp", Address: ln.Addr().String(), TLS: tt.clientTLS, PSK: tt.clientPSK})

			_, _, err := tryDialServer()

			require.Error(t, err)
			var nse noServerError
			assert.NotErrorAs(t, err, &nse, "the daemon answered: it is not absent")
			for _, want := range tt.want {
				assert.Contains(t, err.Error(), want)
			}
			assert.NotContains(t, err.Error(), "cannot reach")

			// soma status --json must fail rather than call it stopped.
			_, err = statusSnapshot()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want[len(tt.want)-1])
		})
	}
}

func TestTryDialServer_RightPairingStillConnects(t *testing.T) {
	ln := listenLoopback(t, false)
	go servePaired(ln, "sesame")
	setEndpoint(t, client.Endpoint{Network: "tcp", Address: ln.Addr().String(), PSK: "sesame"})

	st, err := statusSnapshot()

	require.NoError(t, err)
	assert.Equal(t, protocol.StatusPlaying, st.Status)
}

// TestStatusJSON_ExitsNonZeroWhenTheDaemonRejectsTheKey runs the command
// itself: a daemon that turns the client away may well be playing, so
// status --json must not print a stopped snapshot and exit 0.
func TestStatusJSON_ExitsNonZeroWhenTheDaemonRejectsTheKey(t *testing.T) {
	ln := listenLoopback(t, false)
	go servePaired(ln, "sesame")
	dir := shortTempDir(t)
	pskFile := filepath.Join(dir, "psk")
	require.NoError(t, os.WriteFile(pskFile, []byte("wrong\n"), 0o600))

	out, status := runSoma(t, dir, nil, "--server", ln.Addr().String(), "--psk-file", pskFile, "status", "--json")

	assert.Equal(t, 1, status, "output: %s", out)
	assert.Contains(t, out, "pre-shared key mismatch")
	assert.NotContains(t, out, `"status"`, "no snapshot may claim to know the playback state")
}
