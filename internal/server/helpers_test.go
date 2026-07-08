package server

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/state"

	"github.com/stretchr/testify/require"
)

// mockPlayer is a race-safe test double for the audio.Player interface.
type mockPlayer struct {
	mu        sync.Mutex
	playing   bool
	playErr   error
	playURLs  []string
	volume    float64
	errChan   chan error
	trackChan chan audio.TrackInfo
	// blockPlay, when non-nil, makes Play wait until the channel is closed.
	blockPlay chan struct{}
}

func newMockPlayer() *mockPlayer {
	return &mockPlayer{
		errChan:   make(chan error, 2),
		trackChan: make(chan audio.TrackInfo, 1),
		volume:    1,
	}
}

func (p *mockPlayer) Play(url string) error {
	p.mu.Lock()
	block := p.blockPlay
	p.mu.Unlock()
	if block != nil {
		<-block
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.playErr != nil {
		return p.playErr
	}
	p.playing = true
	p.playURLs = append(p.playURLs, url)
	return nil
}

func (p *mockPlayer) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playing = false
}

func (p *mockPlayer) Errors() <-chan error { return p.errChan }

func (p *mockPlayer) TrackUpdates() <-chan audio.TrackInfo { return p.trackChan }

func (p *mockPlayer) SetVolume(v float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = v
}

func (p *mockPlayer) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

func (p *mockPlayer) setPlayErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playErr = err
}

// testChannels returns a fixed catalog used across server tests.
func testChannels() []channels.Channel {
	return []channels.Channel{
		{
			ID:        "groovesalad",
			Title:     "Groove Salad",
			Playlists: []channels.Playlist{{URL: "http://somafm.com/groovesalad.pls", Format: "mp3"}},
		},
		{
			ID:        "dronezone",
			Title:     "Drone Zone",
			Playlists: []channels.Playlist{{URL: "http://somafm.com/dronezone.pls", Format: "mp3"}},
		},
		{
			ID:        "aacchannel",
			Title:     "AAC Only",
			Playlists: []channels.Playlist{{URL: "http://somafm.com/aac.pls", Format: "aac"}},
		},
	}
}

// newTestServer builds a Server with a mock player, an isolated state dir,
// a fixed catalog, and stream resolution stubbed to skip the network.
func newTestServer(t *testing.T, cfg Config) (*Server, *mockPlayer) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	player := newMockPlayer()
	cfg.Player = player
	if cfg.State == nil {
		cfg.State = &state.State{}
	}
	if cfg.Version == "" {
		cfg.Version = "test"
	}

	prevResolve := resolveStreamURL
	resolveStreamURL = func(playlistURL, _ string) (string, error) {
		return playlistURL + "#stream", nil
	}
	t.Cleanup(func() { resolveStreamURL = prevResolve })

	s := New(cfg)
	s.setCatalog(testChannels())
	t.Cleanup(s.Shutdown)
	return s, player
}

// tclient is a minimal protocol client for driving serveConn in tests.
type tclient struct {
	t      *testing.T
	nc     net.Conn
	mu     sync.Mutex
	nextID int64
	resps  map[int64]chan protocol.Response
	events chan protocol.Event
}

// connect wires a tclient to the server over an in-memory pipe.
func connect(t *testing.T, s *Server) *tclient {
	t.Helper()
	clientSide, serverSide := net.Pipe()
	go s.serveConn(serverSide)

	c := &tclient{
		t:      t,
		nc:     clientSide,
		resps:  make(map[int64]chan protocol.Response),
		events: make(chan protocol.Event, 64),
	}
	go c.readLoop()
	t.Cleanup(func() { _ = clientSide.Close() })
	return c
}

func (c *tclient) readLoop() {
	sc := protocol.NewScanner(c.nc)
	for sc.Scan() {
		var msg protocol.ServerMessage
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.ID != nil {
			c.mu.Lock()
			ch := c.resps[*msg.ID]
			delete(c.resps, *msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- protocol.Response{ID: *msg.ID, Error: msg.Error, Result: msg.Result}
			}
			continue
		}
		c.events <- protocol.Event{Event: msg.Event, Data: msg.Data}
	}
	close(c.events)
}

// call sends a request and waits for its response.
func (c *tclient) call(method string, params any) protocol.Response {
	c.t.Helper()
	var raw json.RawMessage
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		require.NoError(c.t, err)
	}
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan protocol.Response, 1)
	c.resps[id] = ch
	c.mu.Unlock()

	require.NoError(c.t, protocol.WriteLine(c.nc, protocol.Request{ID: id, Method: method, Params: raw}))

	select {
	case resp := <-ch:
		return resp
	case <-time.After(5 * time.Second):
		c.t.Fatalf("timed out waiting for response to %s", method)
		return protocol.Response{}
	}
}

// hello performs the mandatory handshake.
func (c *tclient) hello() protocol.HelloResult {
	c.t.Helper()
	resp := c.call(protocol.MethodHello, protocol.HelloParams{
		ClientVersion:   "test",
		ProtocolVersion: protocol.Version,
	})
	require.Empty(c.t, resp.Error)
	var result protocol.HelloResult
	require.NoError(c.t, json.Unmarshal(resp.Result, &result))
	return result
}

// waitState reads state events until one satisfies the predicate.
func (c *tclient) waitState(desc string, pred func(protocol.PlaybackState) bool) protocol.PlaybackState {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				c.t.Fatalf("connection closed while waiting for state: %s", desc)
			}
			if ev.Event != protocol.EventState {
				continue
			}
			var st protocol.PlaybackState
			require.NoError(c.t, json.Unmarshal(ev.Data, &st))
			if pred(st) {
				return st
			}
		case <-deadline:
			c.t.Fatalf("timed out waiting for state: %s", desc)
		}
	}
}

// waitChannels reads events until a channels payload arrives.
func (c *tclient) waitChannels(desc string) protocol.ChannelsPayload {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-c.events:
			if !ok {
				c.t.Fatalf("connection closed while waiting for channels: %s", desc)
			}
			if ev.Event != protocol.EventChannels {
				continue
			}
			var payload protocol.ChannelsPayload
			require.NoError(c.t, json.Unmarshal(ev.Data, &payload))
			return payload
		case <-deadline:
			c.t.Fatalf("timed out waiting for channels: %s", desc)
		}
	}
}

// decodeState unmarshals a PlaybackState result.
func decodeState(t *testing.T, resp protocol.Response) protocol.PlaybackState {
	t.Helper()
	require.Empty(t, resp.Error)
	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal(resp.Result, &st))
	return st
}
