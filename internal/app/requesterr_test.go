package app

import (
	"errors"
	"testing"

	"somad/internal/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommands_FailedRequestSurfacesInStatusBar(t *testing.T) {
	m := newTestModel(t)
	backend(m).callErr = errors.New("boom")

	msg := runCmd(m.playCmd("groovesalad"))
	require.IsType(t, RequestErrorMsg{}, msg)

	m.Update(msg)
	assert.Contains(t, m.RequestErr, "play failed")
	assert.Contains(t, m.RenderStatusBar(), "play failed")

	// The next successful snapshot clears the notice.
	backend(m).callErr = nil
	m.Update(runCmd(m.fetchStatus()))
	assert.Empty(t, m.RequestErr)
}

func TestCommands_DisconnectedErrorLeftToServerLost(t *testing.T) {
	m := newTestModel(t)
	backend(m).callErr = client.ErrDisconnected

	// Connection loss surfaces via ServerLostMsg from the bridge; the
	// command itself must stay silent to avoid double-reporting.
	assert.Nil(t, runCmd(m.stopCmd()))
	assert.Nil(t, runCmd(m.setVolumeCmd(0.5)))
	assert.Nil(t, runCmd(m.fetchChannels()))
}

func TestUpdate_FirstChannelLoadFailureShowsErrorScreen(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true
	backend(m).callErr = errors.New("catalog exploded")

	m.Update(runCmd(m.fetchChannels()))

	assert.False(t, m.Loading, "a failed first load must not spin forever")
	require.Error(t, m.Err)
	assert.Contains(t, m.Err.Error(), "catalog exploded")
}

func TestUpdate_RestartFailureClearsPendingPlay(t *testing.T) {
	m := newTestModel(t)
	m.pendingPlayID = "groovesalad"
	backend(m).shutdownErr = errors.New("shutdown refused")

	msg := runCmd(m.restartCmd())
	require.IsType(t, RestartFailedMsg{}, msg)

	m.Update(msg)
	assert.Empty(t, m.pendingPlayID, "a stranded pending play would never fire")
	assert.Contains(t, m.RequestErr, "restart failed")
}

func TestUpdate_RestartWithDroppedConnectionStaysSilent(t *testing.T) {
	m := newTestModel(t)
	backend(m).shutdownErr = client.ErrDisconnected

	// A dropped connection means the shutdown worked; the bridge reconnect
	// replays the pending channel change.
	assert.Nil(t, runCmd(m.restartCmd()))
}

func TestToggleFavorite_ServerResultReconcilesOptimisticFlip(t *testing.T) {
	m := newTestModel(t)
	// The server already has a favorite the model does not know about, so
	// the authoritative result must differ from the optimistic flip.
	backend(m).favorites = []string{"dronezone"}

	cmd := m.ToggleFavorite() // selection starts on groovesalad
	msg := runCmd(cmd)
	require.IsType(t, FavoritesMsg{}, msg)

	m.Update(msg)
	assert.ElementsMatch(t, []string{"dronezone", "groovesalad"}, m.Favorites)
}

func TestToggleFavorite_ErrorSurfaces(t *testing.T) {
	m := newTestModel(t)
	backend(m).callErr = errors.New("disk full")

	msg := runCmd(m.ToggleFavorite())
	require.IsType(t, RequestErrorMsg{}, msg)

	m.Update(msg)
	assert.Contains(t, m.RequestErr, "favorite failed")
}
