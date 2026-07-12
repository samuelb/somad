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

// callTimeout bounds a single request/response round trip. Play blocks on
// the network until the stream is decoding, so this is generous.
// A variable so tests can shrink it.
var callTimeout = 30 * time.Second

// Client is a connection to the soma daemon. Safe for concurrent use.
type Client struct {
	nc      net.Conn
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan protocol.Response
	closed  bool

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
	if ep.PSK != "" && !ep.IsLocal() {
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
func (c *Client) Close() error {
	return c.nc.Close()
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
	_ = c.nc.Close()
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

	timeout := time.NewTimer(callTimeout)
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

// Stop halts playback.
func (c *Client) Stop() (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodStop, nil, &st)
	return st, err
}

// SetVolume applies a volume in [0, 1] (the server clamps).
func (c *Client) SetVolume(v float64) (protocol.PlaybackState, error) {
	var st protocol.PlaybackState
	err := c.call(protocol.MethodSetVolume, protocol.SetVolumeParams{Volume: v}, &st)
	return st, err
}

// ToggleFavorite flips a channel's favorite flag and returns the new list.
func (c *Client) ToggleFavorite(channelID string) ([]string, error) {
	var result protocol.FavoritesResult
	err := c.call(protocol.MethodToggleFavorite, protocol.ToggleFavoriteParams{ChannelID: channelID}, &result)
	return result.Favorites, err
}

// Shutdown asks the server to stop playback and exit.
func (c *Client) Shutdown() error {
	return c.call(protocol.MethodShutdown, nil, nil)
}
