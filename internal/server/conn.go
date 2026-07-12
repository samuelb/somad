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

// conn is one client connection. Requests are dispatched concurrently up to
// maxConcurrentRequests; responses and events share a write mutex so lines
// never interleave. Events are delivered through single-slot latest-wins
// channels per event type, so a slow client only ever costs itself
// intermediate snapshots — it can never block the server's broadcast path.
type conn struct {
	s  *Server
	nc net.Conn

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
		sem:        make(chan struct{}, maxConcurrentRequests),
		stateCh:    make(chan protocol.Event, 1),
		channelsCh: make(chan protocol.Event, 1),
		done:       make(chan struct{}),
	}
	// Non-local connections must prove knowledge of the pre-shared key
	// before anything else; the Unix socket is already restricted to the
	// owning user by file permissions. Until then the connection stays
	// unregistered: it receives no state broadcasts and does not keep the
	// server alive past its idle timeout.
	authed := s.psk == "" || isLocalConn(nc)
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
	sc := protocol.NewScanner(nc)
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
			ok := c.handleAuth(req, nonce)
			nonce = nil // single-use: a new attempt needs a new challenge
			if !ok {
				// Slow down brute-force attempts before dropping the
				// connection.
				time.Sleep(time.Duration(authFailureDelay.Load()))
				return
			}
			authed = true
			if !registered {
				if !s.addConn(c) {
					return // the server is shutting down
				}
				registered = true
			}
		case protocol.MethodHello:
			if !authed {
				c.respondError(req.ID, errors.New("authentication required: this server expects a pre-shared key"))
				return
			}
			saidHello = c.handleHello(req)
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

// handleAuth verifies the client's response to the previously issued
// challenge nonce. It reports whether the connection is now authenticated.
func (c *conn) handleAuth(req protocol.Request, nonce []byte) bool {
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
	c.respond(req.ID, struct{}{})
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
	switch req.Method {
	case protocol.MethodStatus:
		c.respond(req.ID, c.s.Snapshot())

	case protocol.MethodChannels:
		c.respond(req.ID, c.s.ChannelsPayload())

	case protocol.MethodPlay:
		var params protocol.PlayParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.respondError(req.ID, fmt.Errorf("malformed play params: %w", err))
			return
		}
		snap, err := c.s.Play(params.ChannelID)
		if err != nil {
			c.respondError(req.ID, err)
			return
		}
		c.respond(req.ID, snap)

	case protocol.MethodPlayPause:
		snap, err := c.s.PlayPause()
		if err != nil {
			c.respondError(req.ID, err)
			return
		}
		c.respond(req.ID, snap)

	case protocol.MethodPlayRelative:
		var params protocol.PlayRelativeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.respondError(req.ID, fmt.Errorf("malformed playRelative params: %w", err))
			return
		}
		snap, err := c.s.PlayRelative(params.Delta)
		if err != nil {
			c.respondError(req.ID, err)
			return
		}
		c.respond(req.ID, snap)

	case protocol.MethodStop:
		c.respond(req.ID, c.s.Stop())

	case protocol.MethodSetVolume:
		var params protocol.SetVolumeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.respondError(req.ID, fmt.Errorf("malformed setVolume params: %w", err))
			return
		}
		c.respond(req.ID, c.s.SetVolume(params.Volume, true))

	case protocol.MethodToggleFavorite:
		var params protocol.ToggleFavoriteParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			c.respondError(req.ID, fmt.Errorf("malformed toggleFavorite params: %w", err))
			return
		}
		favorites, err := c.s.ToggleFavorite(params.ChannelID)
		if err != nil {
			c.respondError(req.ID, err)
			return
		}
		c.respond(req.ID, protocol.FavoritesResult{Favorites: favorites})

	case protocol.MethodShutdown:
		c.respond(req.ID, struct{}{})
		c.s.Shutdown()

	default:
		c.respondError(req.ID, fmt.Errorf("unknown method: %q", req.Method))
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

// write sends one protocol line; a failed write tears the connection down.
func (c *conn) write(v any) {
	c.writeMu.Lock()
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
