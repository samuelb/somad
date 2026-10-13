package main

import (
	"testing"
	"time"

	"somad/internal/app"
	"somad/internal/client"
	"somad/internal/protocol"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// push sends an event to every connection the fake daemon serves.
func (d *fakeDaemon) push(t *testing.T, name string, data any) {
	t.Helper()
	ev, err := protocol.NewEvent(name, data)
	require.NoError(t, err)
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, nc := range d.conns {
		_ = protocol.WriteLine(nc, ev)
	}
}

// dropConns closes every connection, as a daemon that died would.
func (d *fakeDaemon) dropConns() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, nc := range d.conns {
		_ = nc.Close()
	}
	d.conns = nil
}

// bridge runs runBridge on a fresh connection to the fake daemon (whose
// endpoint is current), collecting what it sends the program. Closing the
// returned done ends it; finished is closed once it returned.
func bridge(t *testing.T, shutdownOnExit bool) (msgs <-chan tea.Msg, done chan struct{}, finished <-chan struct{}) {
	t.Helper()
	c, _, err := client.EnsureServer(endpoint, version)
	require.NoError(t, err)
	return bridgeOn(t, c, shutdownOnExit)
}

// bridgeOn is bridge on the connection c; the endpoint it reconnects to is
// whatever is current when it starts, and must not change after.
func bridgeOn(t *testing.T, c *client.Client, shutdownOnExit bool) (msgs <-chan tea.Msg, done chan struct{}, finished <-chan struct{}) {
	t.Helper()
	prevAttempts, prevDelay := reconnectAttempts, reconnectDelay
	reconnectAttempts, reconnectDelay = 2, time.Millisecond

	sent := make(chan tea.Msg, 64)
	done = make(chan struct{})
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		runBridge(func(m tea.Msg) { sent <- m }, c, done, shutdownOnExit)
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			close(done)
		}
		<-fin // it reads the endpoint the cleanups restore
		reconnectAttempts, reconnectDelay = prevAttempts, prevDelay
	})
	return sent, done, fin
}

// nextMsg returns the next message the bridge sent, as a T.
func nextMsg[T tea.Msg](t *testing.T, msgs <-chan tea.Msg) T {
	t.Helper()
	select {
	case m := <-msgs:
		v, ok := m.(T)
		require.Truef(t, ok, "got %T, want %T", m, v)
		return v
	case <-time.After(5 * time.Second):
		var v T
		t.Fatalf("no %T from the bridge", v)
		return v
	}
}

func TestRunBridge_ForwardsEvents(t *testing.T) {
	d := startFakeDaemon(t)
	msgs, _, _ := bridge(t, false)

	d.push(t, protocol.EventState, protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad"})
	assert.Equal(t, "groovesalad", nextMsg[app.ServerStateMsg](t, msgs).State.ChannelID)

	d.push(t, protocol.EventChannels, protocol.ChannelsPayload{Favorites: []string{"dronezone"}})
	assert.Equal(t, []string{"dronezone"}, nextMsg[app.ServerChannelsMsg](t, msgs).Payload.Favorites)

	d.push(t, protocol.EventSpectrum, protocol.SpectrumEvent{Levels: []byte{7, 9}})
	assert.Equal(t, []byte{7, 9}, nextMsg[app.SpectrumMsg](t, msgs).Levels)
}

func TestRunBridge_ReconnectsAfterALostConnection(t *testing.T) {
	d := startFakeDaemon(t)
	msgs, _, _ := bridge(t, false)

	d.dropConns()
	nextMsg[app.ServerLostMsg](t, msgs)
	rec := nextMsg[app.ServerReconnectedMsg](t, msgs)
	assert.NotNil(t, rec.Backend)
	assert.Equal(t, version, rec.ServerVersion)

	// Events now come from the new connection.
	d.push(t, protocol.EventState, protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 0.3})
	assert.InDelta(t, 0.3, nextMsg[app.ServerStateMsg](t, msgs).State.Volume, 1e-9)
}

func TestRunBridge_GivesUpWhenTheServerStaysGone(t *testing.T) {
	d := startFakeDaemon(t)
	c, _, err := client.EnsureServer(endpoint, version)
	require.NoError(t, err)
	// Reconnects go to a remote endpoint that is down; remote endpoints are
	// never spawned, so nothing here starts a daemon.
	setEndpoint(t, client.Endpoint{Network: "tcp", Address: "127.0.0.1:1"})
	msgs, _, finished := bridgeOn(t, c, false)

	d.dropConns()
	nextMsg[app.ServerLostMsg](t, msgs)
	gone := nextMsg[app.ServerGoneMsg](t, msgs)
	assert.ErrorContains(t, gone.Err, "could not restore it")

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge did not end")
	}
}

func TestRunBridge_QuitDuringReconnectShutsTheNewServerDown(t *testing.T) {
	d := startFakeDaemon(t)
	msgs, done, finished := bridge(t, true)

	// The TUI quits while the reconnect is greeting the (possibly freshly
	// spawned) server: with shutdown-on-exit, that server must not be left
	// running.
	d.mu.Lock()
	d.onHello = func() { close(done) }
	d.mu.Unlock()
	d.dropConns()
	nextMsg[app.ServerLostMsg](t, msgs)

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge did not end")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.shutdowns)
	select {
	case m := <-msgs:
		t.Errorf("nothing may reach a program that quit, got %T", m)
	default:
	}
}
