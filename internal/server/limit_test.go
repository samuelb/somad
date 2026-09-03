package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimitListener_CapsOpenConnections(t *testing.T) {
	raw, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := LimitListener(raw, 1)
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- nc
		}
	}()

	dial := func() net.Conn {
		nc, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", ln.Addr().String())
		require.NoError(t, err)
		t.Cleanup(func() { _ = nc.Close() })
		return nc
	}

	dial()
	var first net.Conn
	select {
	case first = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("first connection was not accepted")
	}

	dial()
	select {
	case <-accepted:
		t.Fatal("second connection accepted while the cap is reached")
	case <-time.After(100 * time.Millisecond):
	}

	require.NoError(t, first.Close())
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("closing a connection did not free its slot")
	}
}

func TestLimitListener_CloseUnblocksAccept(t *testing.T) {
	raw, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ln := LimitListener(raw, 1)

	nc, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })
	first, err := ln.Accept()
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })

	// The cap is reached, so Accept is parked on the semaphore; Close must
	// wake it up with an error instead of leaving it hanging.
	errCh := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, ln.Close())
	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
}
