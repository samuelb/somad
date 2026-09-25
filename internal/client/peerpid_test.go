package client

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerPID_ReportsTheListeningProcess(t *testing.T) {
	path := testSocketPath(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	nc, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = nc.Close() })

	pid, err := peerPID(nc)
	if !peerPIDSupported() {
		assert.Error(t, err)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), pid)
}
