package server

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"somatui/internal/protocol"
)

// conn is one client connection. Requests are dispatched concurrently;
// responses and events share a write mutex so lines never interleave. Events
// are delivered through single-slot latest-wins channels per event type, so
// a slow client only ever costs itself intermediate snapshots — it can never
// block the server's broadcast path.
type conn struct {
	s  *Server
	nc net.Conn

	writeMu sync.Mutex

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
		stateCh:    make(chan protocol.Event, 1),
		channelsCh: make(chan protocol.Event, 1),
		done:       make(chan struct{}),
	}
	if !s.addConn(c) {
		_ = nc.Close()
		return
	}
	defer s.removeConn(c)
	defer c.close()

	go c.writeLoop()

	saidHello := false
	sc := protocol.NewScanner(nc)
	for sc.Scan() {
		var req protocol.Request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			c.respondError(req.ID, fmt.Errorf("malformed request: %w", err))
			continue
		}
		// hello must come first and is handled inline so saidHello is set
		// before the next request is read.
		if req.Method == protocol.MethodHello {
			saidHello = c.handleHello(req)
			continue
		}
		if !saidHello {
			c.respondError(req.ID, fmt.Errorf("hello required before %q", req.Method))
			return
		}
		go c.handleRequest(req)
	}
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
		favorites := c.s.ToggleFavorite(params.ChannelID)
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
