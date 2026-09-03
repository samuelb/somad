// Package client implements the soma protocol client used by both the
// TUI and the headless CLI commands: request/response calls over the Unix
// socket (or, for a remote server, TCP with optional TLS and pre-shared-key
// authentication) plus a stream of decoded server events.
package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"somad/internal/protocol"
)

// ErrDisconnected reports that the server connection is gone; pending and
// future calls fail with it.
var ErrDisconnected = errors.New("soma daemon connection lost")

// callTimeout bounds a single request/response round trip for the quick
// methods. A variable so tests can shrink it.
var callTimeout = 30 * time.Second

// playCallTimeout bounds the play-ish calls, which block in the daemon
// until a stream is decoding or every candidate has failed. The daemon's
// worst case per candidate is the 15 s playlist fetch plus the 10 s stream
// connect deadline plus a 30 s stall while priming the decoder (55 s);
// with two candidates and the 15 s audio-device wait that is 125 s, so
// this must exceed it or the client gives up while the daemon goes on to
// succeed on the fallback. A variable so tests can shrink it.
var playCallTimeout = 3 * time.Minute

// Client is a connection to the soma daemon. Safe for concurrent use.
type Client struct {
	nc      net.Conn
	writeMu sync.Mutex

	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan protocol.Response
	closed    bool
	closeOnce sync.Once

	// events carries decoded protocol.PlaybackState and
	// protocol.ChannelsPayload values; closed on disconnect.
	events chan any
}

// Endpoint describes where and how to reach a soma daemon: the local Unix
// socket, or a TCP address with optional TLS and pre-shared-key
// authentication.
type Endpoint struct {
	Network string      // "unix" or "tcp"
	Address string      // socket path, or host:port
	TLS     *tls.Config // nil for a plaintext connection
	PSK     string      // non-empty: authenticate after connecting
}

// UnixEndpoint is the default endpoint: the local Unix socket.
func UnixEndpoint(socketPath string) Endpoint {
	return Endpoint{Network: "unix", Address: socketPath}
}

// IsLocal reports whether the endpoint is the local Unix socket. Only local
// servers can be auto-spawned or restarted for a version upgrade.
func (e Endpoint) IsLocal() bool {
	return e.Network == "unix"
}

func (e Endpoint) String() string {
	if e.IsLocal() {
		return e.Address
	}
	return "tcp://" + e.Address
}

// dialTimeout bounds connecting (and, over TLS, the handshake). Generous
// because remote endpoints cross real networks.
const dialTimeout = 5 * time.Second

// Dial connects to the local server socket. It does not spawn a server and
// does not perform the hello handshake.
func Dial(socketPath string) (*Client, error) {
	return DialEndpoint(UnixEndpoint(socketPath))
}

// DialEndpoint connects to a soma daemon, performing the TLS handshake and
// pre-shared-key authentication when the endpoint asks for them. It does not
// spawn a server and does not perform the hello handshake.
func DialEndpoint(ep Endpoint) (*Client, error) {
	dialer := net.Dialer{Timeout: dialTimeout}
	nc, err := dialer.DialContext(context.Background(), ep.Network, ep.Address)
	if err != nil {
		return nil, err
	}
	if ep.TLS != nil {
		tc := tls.Client(nc, ep.TLS)
		// Handshake now, not lazily on first write: a certificate problem
		// must surface as a connect error, not a mid-request failure.
		ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
		err := tc.HandshakeContext(ctx)
		cancel()
		if err != nil {
			_ = nc.Close()
			return nil, fmt.Errorf("TLS handshake with %s failed: %w", ep, err)
		}
		nc = tc
	}
	c := &Client{
		nc:      nc,
		pending: make(map[int64]chan protocol.Response),
		events:  make(chan any, 32),
	}
	go c.readLoop()
	// A configured PSK always authenticates, regardless of transport: the
	// server is the single source of truth on whether auth is required (it
	// exempts the Unix socket), and it accepts a voluntary handshake even
	// where it is not. Skipping it here based on the transport would let an
	// endpoint that asks for auth silently connect without it.
	if ep.PSK != "" {
		if err := c.authenticate(ep.PSK); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("authenticating with %s: %w", ep, err)
		}
	}
	return c, nil
}

// authenticate proves knowledge of the pre-shared key: it requests a
// challenge nonce and answers with HMAC-SHA256(psk, nonce), so the key never
// travels over the wire.
func (c *Client) authenticate(psk string) error {
	var challenge protocol.AuthChallengeResult
	if err := c.call(protocol.MethodAuthChallenge, nil, &challenge); err != nil {
		return err
	}
	nonce, err := base64.StdEncoding.DecodeString(challenge.Nonce)
	if err != nil {
		return fmt.Errorf("malformed challenge nonce: %w", err)
	}
	mac := protocol.ComputeAuthMAC(psk, nonce)
	return c.call(protocol.MethodAuth, protocol.AuthParams{
		MAC: base64.StdEncoding.EncodeToString(mac),
	}, nil)
}

// Events returns the stream of server-pushed snapshots. The channel is
// closed when the connection is lost.
func (c *Client) Events() <-chan any {
	return c.events
}

// Close tears down the connection; the events channel closes as a result.
// Safe to call multiple times: repeat calls return nil instead of the
// net-package's "use of closed connection" error.
func (c *Client) Close() error {
	err := error(nil)
	c.closeOnce.Do(func() { err = c.nc.Close() })
	return err
}

// readLoop demuxes server lines into pending responses and events.
func (c *Client) readLoop() {
	sc := protocol.NewScanner(c.nc)
	for sc.Scan() {
		var msg protocol.ServerMessage
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.ID != nil {
			c.mu.Lock()
			ch := c.pending[*msg.ID]
			delete(c.pending, *msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- protocol.Response{ID: *msg.ID, Error: msg.Error, Result: msg.Result}
			}
			continue
		}
		c.dispatchEvent(msg)
	}

	// Disconnected: fail everything pending and end the event stream.
	c.mu.Lock()
	c.closed = true
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	close(c.events)
	_ = c.Close()
}

// dispatchEvent decodes and queues one event, dropping the oldest queued
// value when the consumer lags. Events are full snapshots, so dropping
// intermediate ones only skips stale states.
func (c *Client) dispatchEvent(msg protocol.ServerMessage) {
	var ev any
	switch msg.Event {
	case protocol.EventState:
		var st protocol.PlaybackState
		if err := json.Unmarshal(msg.Data, &st); err != nil {
			return
		}
		ev = st
	case protocol.EventChannels:
		var payload protocol.ChannelsPayload
		if err := json.Unmarshal(msg.Data, &payload); err != nil {
			return
		}
		ev = payload
	default:
		return
	}
	for {
		select {
		case c.events <- ev:
			return
		default:
		}
		select {
		case <-c.events:
		default:
		}
	}
}

// call performs one request/response round trip, decoding the result into
// result when it is non-nil.
func (c *Client) call(method string, params any, result any) error {
	var raw json.RawMessage
	if params != nil {
		var err error
		raw, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encoding %s params: %w", method, err)
		}
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrDisconnected
	}
	c.nextID++
	id := c.nextID
	ch := make(chan protocol.Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	c.writeMu.Lock()
	err := protocol.WriteLine(c.nc, protocol.Request{ID: id, Method: method, Params: raw})
	c.writeMu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%w: %v", ErrDisconnected, err)
	}

	timeout := time.NewTimer(callTimeoutFor(method))
	defer timeout.Stop()

	select {
	case resp, ok := <-ch:
		if !ok {
			return ErrDisconnected
		}
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		if result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("decoding %s result: %w", method, err)
			}
		}
		return nil
	case <-timeout.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("timed out waiting for %s response", method)
	}
}

// callTimeoutFor returns the round-trip bound for a method.
func callTimeoutFor(method string) time.Duration {
	switch method {
	case protocol.MethodPlay, protocol.MethodPlayPause, protocol.MethodPlayRelative:
		return playCallTimeout
	default:
		return callTimeout
	}
}

// Hello performs the mandatory handshake.
func (c *Client) Hello(clientVersion string) (protocol.HelloResult, error) {
	var result protocol.HelloResult
	err := c.call(protocol.MethodHello, protocol.HelloParams{
		ClientVersion:   clientVersion,
		ProtocolVersion: protocol.Version,
	}, &result)
	return result, err
}

// Status returns the current playback snapshot.
func (c *Client) Status() (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodStatus, nil, &st)
	return st, err
}

// Channels returns the catalog with favorites and the last-played channel.
func (c *Client) Channels() (protocol.ChannelsPayload, error) {
	var payload protocol.ChannelsPayload
	err := c.call(protocol.MethodChannels, nil, &payload)
	return payload, err
}

// Play starts a channel, blocking until it is connected or has failed.
func (c *Client) Play(channelID string) (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: channelID}, &st)
	return st, err
}

// PlayPause toggles between stopped and playing (live radio has no real
// pause: unpausing reconnects to the live stream).
func (c *Client) PlayPause() (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodPlayPause, nil, &st)
	return st, err
}

// PlayRelative plays the channel delta positions away from the current (or
// last played) one in catalog order: +1 for next, -1 for previous.
func (c *Client) PlayRelative(delta int) (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodPlayRelative, protocol.PlayRelativeParams{Delta: delta}, &st)
	return st, err
}

// Stop halts playback immediately and cancels any pending sleep timer.
func (c *Client) Stop() (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodStop, nil, &st)
	return st, err
}

// StopIn arms a sleep timer that stops playback after d instead of
// immediately, replacing any timer already pending. The daemon owns the
// timer, so it fires even after this client disconnects.
func (c *Client) StopIn(d time.Duration) (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodStop, protocol.StopParams{In: d.String()}, &st)
	return st, err
}

// CancelStop cancels a pending sleep timer without stopping playback.
func (c *Client) CancelStop() (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodStop, protocol.StopParams{Cancel: true}, &st)
	return st, err
}

// SetVolume applies a volume in [0, 1] (the server clamps).
func (c *Client) SetVolume(v float64) (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodSetVolume, protocol.SetVolumeParams{Volume: v}, &st)
	return st, err
}

// ToggleMute mutes playback, remembering the current volume to restore, or
// restores it (or a sensible default) when already muted.
func (c *Client) ToggleMute() (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodToggleMute, nil, &st)
	return st, err
}

// ToggleFavorite flips a channel's favorite flag and returns the new list.
func (c *Client) ToggleFavorite(channelID string) ([]string, error) {
	var result protocol.FavoritesResult
	err := c.call(protocol.MethodToggleFavorite, protocol.ToggleFavoriteParams{ChannelID: channelID}, &result)
	return result.Favorites, err
}

// History returns recent now-playing titles, newest first. channelID empty
// returns entries across every channel played this daemon lifetime; a
// non-empty one filters to it and lets the server backfill from SomaFM's own
// song history. limit <= 0 uses the server's default.
func (c *Client) History(channelID string, limit int) ([]protocol.HistoryEntry, error) {
	var result protocol.HistoryResult
	err := c.call(protocol.MethodHistory, protocol.HistoryParams{ChannelID: channelID, Limit: limit}, &result)
	return result.Entries, err
}

// ReloadLastfm asks the daemon to re-read the Last.fm session key (the
// config's lastfm.session_key override, else internal/state's persisted
// lastfm.json), so a session obtained by "soma lastfm login" takes effect
// without restarting the daemon.
func (c *Client) ReloadLastfm() error {
	return c.call(protocol.MethodReloadLastfm, nil, nil)
}

// Shutdown asks the server to stop playback and exit.
func (c *Client) Shutdown() error {
	return c.call(protocol.MethodShutdown, nil, nil)
}
