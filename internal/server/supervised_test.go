package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"somad/internal/protocol"
)

// A supervised daemon says so in its hello and takes a shutdown that asks
// for a restart as a request to exit so that its service manager starts it
// again (ADR-0038); stopped for good, it asks for nothing.
func TestShutdown_ASupervisedDaemonNotesARestartAskedFor(t *testing.T) {
	for name, tc := range map[string]struct {
		supervised bool
		params     any
		want       bool
	}{
		"supervised, for a restart": {supervised: true, params: protocol.ShutdownParams{Restart: true}, want: true},
		"supervised, for good":      {supervised: true},
		"supervised, empty params":  {supervised: true, params: protocol.ShutdownParams{}},
		"on its own, for a restart": {params: protocol.ShutdownParams{Restart: true}},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestServer(t, Config{Supervised: tc.supervised})
			c := connect(t, s)
			assert.Equal(t, tc.supervised, c.hello().Supervised)

			resp := c.call(protocol.MethodShutdown, tc.params)
			require.Empty(t, resp.Error)
			<-s.done

			assert.Equal(t, tc.want, s.RestartRequested())
		})
	}
}

func TestShutdown_MalformedParamsAreRefused(t *testing.T) {
	s, _ := newTestServer(t, Config{Supervised: true})
	c := connect(t, s)
	c.hello()

	resp := c.call(protocol.MethodShutdown, map[string]any{"restart": "yes"})

	assert.Contains(t, resp.Error, "malformed shutdown params")
	assert.False(t, s.RestartRequested())
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.False(t, s.closing, "a request the daemon cannot read stops nothing")
}
