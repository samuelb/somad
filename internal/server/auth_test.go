package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/tlsutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shrinkAuthFailureDelay makes failed-auth tests fast.
func shrinkAuthFailureDelay(t *testing.T) {
	t.Helper()
	prev := authFailureDelay.Load()
	authFailureDelay.Store(int64(time.Millisecond))
	t.Cleanup(func() { authFailureDelay.Store(prev) })
}

// authenticate runs the challenge–response exchange with the given key and
// returns the auth response.
func (c *tclient) authenticate(psk string) protocol.Response {
	c.t.Helper()
	resp := c.call(protocol.MethodAuthChallenge, nil)
	require.Empty(c.t, resp.Error)
	var challenge protocol.AuthChallengeResult
	require.NoError(c.t, json.Unmarshal(resp.Result, &challenge))
	nonce, err := base64.StdEncoding.DecodeString(challenge.Nonce)
	require.NoError(c.t, err)
	return c.call(protocol.MethodAuth, protocol.AuthParams{
		MAC: base64.StdEncoding.EncodeToString(protocol.ComputeAuthMAC(psk, nonce)),
	})
}

// waitClosed asserts the server closes the connection (the event stream ends).
func (c *tclient) waitClosed(desc string) {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.events:
			if !ok {
				return
			}
		case <-deadline:
			c.t.Fatalf("connection still open: %s", desc)
		}
	}
}

func TestAuth_RequiredBeforeHelloOnNonLocalConns(t *testing.T) {
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s) // net.Pipe: not a Unix socket, so auth applies

	resp := c.call(protocol.MethodHello, protocol.HelloParams{ClientVersion: "test", ProtocolVersion: protocol.Version})
	assert.Contains(t, resp.Error, "authentication required")
	c.waitClosed("after unauthenticated hello")
}

func TestAuth_RequiredBeforeOtherMethods(t *testing.T) {
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s)

	resp := c.call(protocol.MethodStatus, nil)
	assert.Contains(t, resp.Error, "authentication required")
	c.waitClosed("after unauthenticated status")
}

func TestAuth_ChallengeResponseSucceeds(t *testing.T) {
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s)

	resp := c.authenticate("secret")
	require.Empty(t, resp.Error)
	hr := c.hello()
	assert.Equal(t, "test", hr.ServerVersion)
	resp = c.call(protocol.MethodStatus, nil)
	assert.Empty(t, resp.Error)
}

func TestAuth_WrongKeyClosesConnection(t *testing.T) {
	shrinkAuthFailureDelay(t)
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s)

	resp := c.authenticate("wrong")
	assert.Contains(t, resp.Error, "pre-shared key mismatch")
	c.waitClosed("after failed auth")
}

func TestAuth_WithoutChallengeFails(t *testing.T) {
	shrinkAuthFailureDelay(t)
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s)

	resp := c.call(protocol.MethodAuth, protocol.AuthParams{MAC: base64.StdEncoding.EncodeToString([]byte("x"))})
	assert.Contains(t, resp.Error, "authChallenge")
	c.waitClosed("auth without a challenge")
}

// unixAddrConn disguises a pipe as a Unix-socket connection.
type unixAddrConn struct{ net.Conn }

func (unixAddrConn) RemoteAddr() net.Addr { return &net.UnixAddr{Name: "test.sock", Net: "unix"} }

func TestAuth_UnixConnectionsAreExempt(t *testing.T) {
	s, _ := newTestServer(t, Config{PSK: "secret"})

	clientSide, serverSide := net.Pipe()
	go s.serveConn(unixAddrConn{serverSide})
	c := &tclient{
		t:      t,
		nc:     clientSide,
		resps:  make(map[int64]chan protocol.Response),
		events: make(chan protocol.Event, 64),
	}
	go c.readLoop()
	t.Cleanup(func() { _ = clientSide.Close() })

	// hello straight away, no auth: the Unix socket is permission-protected.
	hr := c.hello()
	assert.Equal(t, "test", hr.ServerVersion)
}

func TestAuth_NoPSKConfiguredAcceptsAuthenticatingClients(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)

	// A client configured with a key must still work against a server that
	// does not require one.
	resp := c.authenticate("whatever")
	assert.Empty(t, resp.Error)
	c.hello()
}

// TestRemoteEndToEnd exercises the full remote stack: the real protocol
// client over TCP with TLS (fingerprint-pinned) and PSK authentication.
func TestRemoteEndToEnd(t *testing.T) {
	shrinkAuthFailureDelay(t)
	s, _ := newTestServer(t, Config{PSK: "secret"})

	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	_, err := tlsutil.EnsureServerCert(certPath, keyPath, nil)
	require.NoError(t, err)
	serverCfg, fingerprint, err := tlsutil.ServerTLSConfig(certPath, keyPath)
	require.NoError(t, err)

	tcpLn, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := tls.NewListener(tcpLn, serverCfg)
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = s.acceptLoop(ln) }()

	clientCfg, err := tlsutil.ClientTLSConfig("", fingerprint, "127.0.0.1")
	require.NoError(t, err)
	ep := client.Endpoint{Network: "tcp", Address: tcpLn.Addr().String(), TLS: clientCfg, PSK: "secret"}

	c, err := client.DialEndpoint(ep)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	hr, err := c.Hello("test")
	require.NoError(t, err)
	assert.Equal(t, "test", hr.ServerVersion)
	st, err := c.Status()
	require.NoError(t, err)
	assert.Equal(t, protocol.StatusStopped, st.Status)

	// The wrong key must not get a connection.
	bad := ep
	bad.PSK = "wrong"
	_, err = client.DialEndpoint(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pre-shared key mismatch")
}

// expectNoEvent asserts that no event arrives within a short window.
func (c *tclient) expectNoEvent(desc string) {
	c.t.Helper()
	select {
	case ev, ok := <-c.events:
		if ok {
			c.t.Fatalf("unexpected event %q: %s", ev.Event, desc)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

func TestAuth_UnauthenticatedConnsReceiveNoBroadcasts(t *testing.T) {
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s)

	// A broadcast triggered while the connection has not authenticated must
	// not reach it: state snapshots leak what is playing.
	s.setCatalog(testChannels())
	c.expectNoEvent("broadcast to an unauthenticated connection")

	// After authenticating, the same broadcast arrives.
	require.Empty(t, c.authenticate("secret").Error)
	s.setCatalog(testChannels())
	c.waitChannels("broadcast after authenticating")
}

func TestAuth_UnauthenticatedConnsDoNotBlockIdleExit(t *testing.T) {
	s, _ := newTestServer(t, Config{PSK: "secret", IdleTimeout: 30 * time.Millisecond})
	connect(t, s) // never authenticates

	s.mu.Lock()
	s.maybeArmIdleLocked()
	s.mu.Unlock()

	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("an unauthenticated connection must not keep the server alive past its idle timeout")
	}
}

// shrinkHandshakeTimeout makes handshake-deadline tests fast.
func shrinkHandshakeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := handshakeTimeout.Load()
	handshakeTimeout.Store(int64(d))
	t.Cleanup(func() { handshakeTimeout.Store(prev) })
}

func TestConn_HandshakeDeadlineDropsSilentRemotePeer(t *testing.T) {
	shrinkHandshakeTimeout(t, 50*time.Millisecond)
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s) // net.Pipe: not a Unix socket, so it counts as remote

	// Say nothing at all: the server must not wait for us forever.
	c.waitClosed("silent peer past the handshake deadline")
}

func TestConn_HandshakeDeadlineDropsPeerStuckBeforeHello(t *testing.T) {
	shrinkHandshakeTimeout(t, 50*time.Millisecond)
	s, _ := newTestServer(t, Config{PSK: "secret"})
	c := connect(t, s)

	// Authenticated but never says hello: still within the handshake.
	resp := c.authenticate("secret")
	require.Empty(t, resp.Error)
	c.waitClosed("authenticated peer that never says hello")
}

func TestConn_HelloClearsHandshakeDeadline(t *testing.T) {
	shrinkHandshakeTimeout(t, 50*time.Millisecond)
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	// An idle client after hello (a TUI waiting for events) must stay
	// connected well past the handshake deadline.
	time.Sleep(150 * time.Millisecond)
	resp := c.call(protocol.MethodStatus, nil)
	assert.Empty(t, resp.Error)
}

func TestConn_OversizedRequestLineClosesConnection(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	c := connect(t, s)
	c.hello()

	line := bytes.Repeat([]byte{'x'}, protocol.MaxRequestBytes+1)
	line = append(line, '\n')
	// The server stops reading once the line exceeds its cap, so this write
	// may fail partway through on the pipe; either way the connection ends.
	_, _ = c.nc.Write(line)
	c.waitClosed("request line over MaxRequestBytes")
}

func TestConn_WriteDeadlineDropsPeerThatStopsReading(t *testing.T) {
	prev := writeTimeout.Load()
	writeTimeout.Store(int64(50 * time.Millisecond))
	t.Cleanup(func() { writeTimeout.Store(prev) })
	s, _ := newTestServer(t, Config{})

	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })
	done := make(chan struct{})
	go func() {
		s.serveConn(serverSide)
		close(done)
	}()

	// Send a request and never read the response: on a pipe the server's
	// write blocks until we read, so only the deadline can end it.
	require.NoError(t, protocol.WriteLine(clientSide, protocol.Request{ID: 1, Method: protocol.MethodStatus}))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server kept the connection despite the stuck write")
	}
}

// A peer that sends requests and never reads the answers parks the server
// in a write. Before hello that must end with the handshake deadline, not
// with the three times longer write timeout.
func TestConn_HandshakeDeadlineBoundsWritesBeforeHello(t *testing.T) {
	shrinkHandshakeTimeout(t, 50*time.Millisecond)
	s, _ := newTestServer(t, Config{PSK: "secret"})

	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })
	done := make(chan struct{})
	go func() {
		s.serveConn(serverSide)
		close(done)
	}()

	require.NoError(t, protocol.WriteLine(clientSide, protocol.Request{ID: 1, Method: protocol.MethodAuthChallenge}))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server kept the connection past the handshake deadline on a stuck write")
	}
}

// A browser will POST to a loopback port for any page that asks. The request
// line and headers used to be skipped as bad requests, leaving the JSON
// lines in the body to run; the connection must end at the request line.
func TestConn_HTTPRequestIsDroppedAtItsFirstLine(t *testing.T) {
	s, _ := newTestServer(t, Config{}) // a loopback listener with no PSK
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = s.acceptLoop(ln) }()
	before := s.Snapshot().Volume
	require.NotEqual(t, 0.25, before)

	nc, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })

	var body bytes.Buffer
	require.NoError(t, protocol.WriteLine(&body, protocol.Request{
		ID: 1, Method: protocol.MethodHello,
		Params: json.RawMessage(fmt.Sprintf(`{"clientVersion":"web","protocolVersion":%d}`, protocol.Version)),
	}))
	require.NoError(t, protocol.WriteLine(&body, protocol.Request{
		ID: 2, Method: protocol.MethodSetVolume, Params: json.RawMessage(`{"volume":0.25}`),
	}))
	_, err = fmt.Fprintf(nc, "POST / HTTP/1.1\r\nHost: %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		ln.Addr(), body.Len(), body.String())
	require.NoError(t, err)

	// The server hangs up. A reset (it closed with the rest of the request
	// unread) is as good as an EOF; only a timeout means it is still there.
	require.NoError(t, nc.SetReadDeadline(time.Now().Add(2*time.Second)))
	reply, err := io.ReadAll(nc)
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(), "connection still open, read %q", reply)
	assert.NotContains(t, string(reply), `"result"`, "no request in the body may be answered")
	assert.Equal(t, before, s.Snapshot().Volume, "the setVolume in the body must not run")
}

func TestConn_MalformedLineIsAnsweredOnceThenDropped(t *testing.T) {
	s, _ := newTestServer(t, Config{})
	clientSide, serverSide := net.Pipe()
	t.Cleanup(func() { _ = clientSide.Close() })
	go s.serveConn(serverSide)
	require.NoError(t, clientSide.SetDeadline(time.Now().Add(2*time.Second)))

	// The pipe is unbuffered and the server stops reading after the first
	// line, so the second is written from its own goroutine.
	_, err := clientSide.Write([]byte("GET / HTTP/1.1\r\n"))
	require.NoError(t, err)
	go func() { _, _ = clientSide.Write([]byte("Host: localhost\r\n")) }()

	sc := protocol.NewScanner(clientSide)
	require.True(t, sc.Scan(), "the one error reply")
	assert.Contains(t, sc.Text(), "malformed request")
	assert.False(t, sc.Scan(), "the connection ends after it, got %q", sc.Text())
	assert.NoError(t, sc.Err(), "closed by the server, not timed out")
}
