package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"somad/internal/protocol"
)

// maxConcurrentRequests caps how many of one connection's requests are
// dispatched at once, so a client sending requests faster than they can be
// handled applies backpressure on its own read loop instead of spawning
// unbounded goroutines.
const maxConcurrentRequests = 32

// Deadlines for TCP connections (in nanoseconds; atomics so tests can shrink
// them without racing lingering connection goroutines). The Unix socket is
// exempt: its directory is already restricted to the owning user, so a
// deadline there would only guard against a buggy local client.
//
// handshakeTimeout bounds everything up to a successful hello, including a
// lazy TLS handshake and the PSK exchange, so an unauthenticated peer cannot
// hold a connection, its goroutines, and its scanner buffer indefinitely.
// After hello, reads are unbounded: an idle TUI legitimately sits on the
// connection for hours without sending anything, and dead peers are reaped
// by TCP keepalive (on by default for accepted connections).
//
// writeTimeout bounds each write so a peer that stops reading cannot park
// the writer holding writeMu forever; the connection is dropped instead.
var (
	handshakeTimeout = newDurationAtomic(10 * time.Second)
	writeTimeout     = newDurationAtomic(30 * time.Second)
)

func newDurationAtomic(d time.Duration) *atomic.Int64 {
	a := &atomic.Int64{}
	a.Store(int64(d))
	return a
}

// conn is one client connection. Requests are dispatched concurrently up to
// maxConcurrentRequests; responses and events share a write mutex so lines
// never interleave. Events are delivered through single-slot latest-wins
// channels per event type, so a slow client only ever costs itself
// intermediate snapshots — it can never block the server's broadcast path.
type conn struct {
	s  *Server
	nc net.Conn
	// remote is true for TCP connections, which get deadlines; the Unix
	// socket does not.
	remote bool

	writeMu sync.Mutex
	sem     chan struct{}

	stateCh    chan protocol.Event
	channelsCh chan protocol.Event

	closeOnce sync.Once
	done      chan struct{}
}

// serveConn owns the connection's lifecycle: registration, the read loop,
// and teardown.
func (s *Server) serveConn(nc net.Conn) {
	c := &conn{
		s:          s,
		nc:         nc,
		remote:     !isLocalConn(nc),
		sem:        make(chan struct{}, maxConcurrentRequests),
		stateCh:    make(chan protocol.Event, 1),
		channelsCh: make(chan protocol.Event, 1),
		done:       make(chan struct{}),
	}
	if c.remote {
		// Covers the (lazy) TLS handshake, authentication, and hello;
		// cleared once hello succeeds. See handshakeTimeout.
		_ = nc.SetReadDeadline(time.Now().Add(time.Duration(handshakeTimeout.Load())))
	}
	// Non-local connections must prove knowledge of the pre-shared key
	// before anything else; the Unix socket is already restricted to the
	// owning user by file permissions. Until then the connection stays
	// unregistered: it receives no state broadcasts and does not keep the
	// server alive past its idle timeout.
	authed := !c.remote || s.psk == ""
	registered := false
	defer func() {
		if registered {
			s.removeConn(c)
		}
		c.close()
	}()
	if authed {
		if !s.addConn(c) {
			return
		}
		registered = true
	}

	go c.writeLoop()

	var nonce []byte
	saidHello := false
	// The request scanner is far smaller than the client's: every
	// client-to-server line is tiny, and this bounds what a peer can make
	// the server buffer before it is authenticated.
	sc := protocol.NewRequestScanner(nc)
	for sc.Scan() {
		var req protocol.Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			c.respondError(req.ID, fmt.Errorf("malformed request: %w", err))
			continue
		}
		// auth and hello are handled inline so authed/saidHello are set
		// before the next request is read.
		switch req.Method {
		case protocol.MethodAuthChallenge:
			var err error
			if nonce, err = protocol.NewAuthNonce(); err != nil {
				c.respondError(req.ID, err)
				return
			}
			c.respond(req.ID, protocol.AuthChallengeResult{Nonce: base64.StdEncoding.EncodeToString(nonce)})
		case protocol.MethodAuth:
			ok := c.verifyAuth(req, nonce)
			nonce = nil // single-use: a new attempt needs a new challenge
			if !ok {
				// Slow down brute-force attempts before dropping the
				// connection.
				time.Sleep(time.Duration(authFailureDelay.Load()))
				return
			}
			authed = true
			if c.remote {
				// Auth succeeded: give hello a fresh window instead of
				// whatever is left of the one absolute deadline.
				_ = nc.SetReadDeadline(time.Now().Add(time.Duration(handshakeTimeout.Load())))
			}
			if !registered {
				if !s.addConn(c) {
					return // the server is shutting down
				}
				registered = true
			}
			// Respond only after registering: a client that acts on this
			// response must already be receiving broadcasts.
			c.respond(req.ID, struct{}{})
		case protocol.MethodHello:
			if !authed {
				c.respondError(req.ID, errors.New("authentication required: this server expects a pre-shared key"))
				return
			}
			saidHello = c.handleHello(req)
			if saidHello && c.remote {
				_ = nc.SetReadDeadline(time.Time{})
			}
		default:
			if !authed {
				c.respondError(req.ID, fmt.Errorf("authentication required before %q", req.Method))
				return
			}
			if !saidHello {
				c.respondError(req.ID, fmt.Errorf("hello required before %q", req.Method))
				return
			}
			// The blocking send is the intended backpressure on a client
			// with maxConcurrentRequests in flight — but during teardown
			// nothing will free a slot, so bail out instead of holding the
			// read loop until a handler happens to finish.
			select {
			case c.sem <- struct{}{}:
			case <-c.done:
				return
			case <-c.s.done:
				return
			}
			go func() {
				defer func() { <-c.sem }()
				c.handleRequest(req)
			}()
		}
	}
}

// authFailureDelay is how long (in nanoseconds) a failed authentication
// stalls before the connection closes. Atomic so tests can shrink it without
// racing lingering connection goroutines.
var authFailureDelay = func() *atomic.Int64 {
	d := &atomic.Int64{}
	d.Store(int64(time.Second))
	return d
}()

// isLocalConn reports whether the connection arrived over the Unix socket
// (as opposed to TCP, possibly TLS-wrapped, whose RemoteAddr network stays
// "tcp").
func isLocalConn(nc net.Conn) bool {
	return nc.RemoteAddr().Network() == "unix"
}

// verifyAuth checks the client's response to the previously issued challenge
// nonce and reports whether the connection is now authenticated. Failures are
// answered inline; on success the caller sends the response after it has
// registered the connection for broadcasts.
func (c *conn) verifyAuth(req protocol.Request, nonce []byte) bool {
	var params protocol.AuthParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.respondError(req.ID, fmt.Errorf("malformed auth params: %w", err))
		return false
	}
	if nonce == nil {
		c.respondError(req.ID, errors.New("auth requires a preceding authChallenge"))
		return false
	}
	mac, err := base64.StdEncoding.DecodeString(params.MAC)
	if err != nil {
		c.respondError(req.ID, fmt.Errorf("malformed auth mac: %w", err))
		return false
	}
	// With no key configured the server does not require authentication, so
	// an authenticating client simply passes.
	if c.s.psk != "" && !protocol.VerifyAuthMAC(c.s.psk, nonce, mac) {
		c.respondError(req.ID, errors.New("authentication failed: pre-shared key mismatch"))
		return false
	}
	return true
}

// handleHello verifies protocol compatibility. It reports whether the
// connection may proceed.
func (c *conn) handleHello(req protocol.Request) bool {
	var params protocol.HelloParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.respondError(req.ID, fmt.Errorf("malformed hello params: %w", err))
		return false
	}
	if params.ProtocolVersion != protocol.Version {
		c.respondError(req.ID, fmt.Errorf(
			"incompatible protocol version: server speaks %d, client speaks %d",
			protocol.Version, params.ProtocolVersion))
		return false
	}
	c.respond(req.ID, protocol.HelloResult{
		ServerVersion:   c.s.version,
		ProtocolVersion: protocol.Version,
		PID:             os.Getpid(),
	})
	return true
}

// handleRequest dispatches one post-hello request. It runs on its own
// goroutine, so a blocking play never stalls other requests from the same
// client.
func (c *conn) handleRequest(req protocol.Request) {
	reply := c.replier(req.ID)
	switch req.Method {
	case protocol.MethodStatus:
		c.respond(req.ID, c.s.Snapshot())

	case protocol.MethodChannels:
		c.respond(req.ID, c.s.ChannelsPayload())

	case protocol.MethodPlay:
		if params, ok := decodeParams[protocol.PlayParams](c, req); ok {
			reply(c.s.Play(params.ChannelID))
		}

	case protocol.MethodPlayPause:
		reply(c.s.PlayPause())

	case protocol.MethodPlayRelative:
		if params, ok := decodeParams[protocol.PlayRelativeParams](c, req); ok {
			reply(c.s.PlayRelative(params.Delta))
		}

	case protocol.MethodStop:
		var params protocol.StopParams
		if len(req.Params) > 0 { // a bare stop carries no params at all
			var ok bool
			if params, ok = decodeParams[protocol.StopParams](c, req); !ok {
				return
			}
		}
		reply(c.stop(params))

	case protocol.MethodSetVolume:
		if params, ok := decodeParams[protocol.SetVolumeParams](c, req); ok {
			c.respond(req.ID, c.s.SetVolume(params.Volume, true))
		}

	case protocol.MethodToggleMute:
		c.respond(req.ID, c.s.ToggleMute())

	case protocol.MethodToggleFavorite:
		if params, ok := decodeParams[protocol.ToggleFavoriteParams](c, req); ok {
			favorites, err := c.s.ToggleFavorite(params.ChannelID)
			reply(protocol.FavoritesResult{Favorites: favorites}, err)
		}

	case protocol.MethodHistory:
		if params, ok := decodeParams[protocol.HistoryParams](c, req); ok {
			c.respond(req.ID, protocol.HistoryResult{Entries: c.s.History(params.ChannelID, params.Limit)})
		}

	case protocol.MethodReloadLastfm:
		reply(struct{}{}, c.s.ReloadLastfm())

	case protocol.MethodShutdown:
		c.respond(req.ID, struct{}{})
		c.s.Shutdown()

	default:
		c.respondError(req.ID, fmt.Errorf("unknown method: %q", req.Method))
	}
}

// stop maps the stop request's three forms (stop now, arm a sleep timer,
// cancel a pending one) onto the server, validating the wire's duration
// string on the way.
func (c *conn) stop(params protocol.StopParams) (protocol.PlaybackState, error) {
	switch {
	case params.Cancel:
		return c.s.CancelPendingStop(), nil
	case params.In != "":
		d, err := time.ParseDuration(params.In)
		if err != nil {
			return protocol.PlaybackState{}, fmt.Errorf("malformed stop \"in\" duration: %w", err)
		}
		if d <= 0 {
			return protocol.PlaybackState{}, errors.New(`stop "in" duration must be positive`)
		}
		return c.s.StopIn(d), nil
	default:
		return c.s.Stop(), nil
	}
}

// decodeParams unmarshals req.Params as a T. On failure it answers the
// request with a malformed-params error itself and reports false.
func decodeParams[T any](c *conn, req protocol.Request) (T, bool) {
	var params T
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.respondError(req.ID, fmt.Errorf("malformed %s params: %w", req.Method, err))
		return params, false
	}
	return params, true
}

// replier returns the answer for request id: result, or err when the call
// failed. Bound to the id so a (value, error) call can be passed straight
// through, e.g. reply(c.s.Play(id)).
func (c *conn) replier(id int64) func(result any, err error) {
	return func(result any, err error) {
		if err != nil {
			c.respondError(id, err)
			return
		}
		c.respond(id, result)
	}
}

func (c *conn) respond(id int64, result any) {
	raw, err := json.Marshal(result)
	if err != nil {
		c.respondError(id, fmt.Errorf("encoding result: %w", err))
		return
	}
	c.write(protocol.Response{ID: id, Result: raw})
}

func (c *conn) respondError(id int64, err error) {
	c.write(protocol.Response{ID: id, Error: err.Error()})
}

// sendEvent queues an event for delivery, replacing any pending event of the
// same type so the newest snapshot wins. Never blocks.
func (c *conn) sendEvent(ev protocol.Event) {
	ch := c.stateCh
	if ev.Event == protocol.EventChannels {
		ch = c.channelsCh
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- ev:
	default:
	}
}

// writeLoop delivers queued events until the connection closes.
func (c *conn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case ev := <-c.stateCh:
			c.write(ev)
		case ev := <-c.channelsCh:
			c.write(ev)
		}
	}
}

// write sends one protocol line; a failed write (a TCP peer that stops
// reading for writeTimeout included) tears the connection down.
func (c *conn) write(v any) {
	c.writeMu.Lock()
	if c.remote {
		_ = c.nc.SetWriteDeadline(time.Now().Add(time.Duration(writeTimeout.Load())))
	}
	err := protocol.WriteLine(c.nc, v)
	c.writeMu.Unlock()
	if err != nil {
		c.close()
	}
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.nc.Close()
	})
}
