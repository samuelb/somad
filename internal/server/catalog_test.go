package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"somad/internal/channels"
	"somad/internal/security/securitytest"
	"somad/internal/state"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBareServer builds a Server like newTestServer, but without pre-seeding
// the catalog, so loadCatalog/refreshCatalog's own effects are observable.
func newBareServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := New(Config{Player: newMockPlayer(), State: &state.State{}, Version: "test"})
	t.Cleanup(s.Shutdown)
	return s
}

// stubChannelsNetwork points the channels package at an httptest server for
// the duration of the test and returns it.
func stubChannelsNetwork(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	securitytest.AllowTestHosts(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	prev := channels.SomaFMChannelsURL
	channels.SomaFMChannelsURL = srv.URL
	t.Cleanup(func() { channels.SomaFMChannelsURL = prev })
	return srv
}

func TestLoadCatalog_SeedsFromCacheThenRefreshesFromNetwork(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cached := &channels.Channels{Channels: []channels.Channel{{ID: "cached-only", Title: "Cached Only"}}}
	require.NoError(t, channels.WriteChannelsToCache(cached))

	release := make(chan struct{})
	fresh := channels.Channels{Channels: testChannels()}
	stubChannelsNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
		data, _ := json.Marshal(fresh)
		_, _ = w.Write(data)
	})

	s := newBareServer(t)
	s.loadCatalog()

	// The cache seeds the catalog synchronously; the network refresh is
	// still blocked on `release` and cannot have completed yet.
	payload := s.ChannelsPayload()
	require.Len(t, payload.Channels, 1)
	assert.Equal(t, "cached-only", payload.Channels[0].ID)

	close(release)
	require.Eventually(t, func() bool {
		return len(s.ChannelsPayload().Channels) == len(testChannels())
	}, 2*time.Second, 10*time.Millisecond, "catalog should refresh from the network")
}

func TestRefreshCatalog_NetworkFailureSurfacesErrorWhenCatalogEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stubChannelsNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	s := newBareServer(t)
	s.refreshCatalog()

	payload := s.ChannelsPayload()
	assert.Empty(t, payload.Channels)
	assert.Contains(t, payload.Error, "500")
}

func TestRefreshCatalog_NetworkFailureIsSilentWhenCatalogNonEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stubChannelsNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	s := newBareServer(t)
	s.setCatalog(testChannels())

	s.refreshCatalog()

	payload := s.ChannelsPayload()
	assert.Len(t, payload.Channels, len(testChannels()))
	assert.Empty(t, payload.Error, "a prior successful catalog must not be clobbered by a background refresh failure")
}

func TestRefreshLoop_PeriodicallyRefreshesCatalog(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	prev := channelRefreshInterval
	channelRefreshInterval = time.Millisecond
	t.Cleanup(func() { channelRefreshInterval = prev })

	hits := make(chan struct{}, 8)
	fresh := channels.Channels{Channels: testChannels()}
	stubChannelsNetwork(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case hits <- struct{}{}:
		default:
		}
		data, _ := json.Marshal(fresh)
		_, _ = w.Write(data)
	})

	s := newBareServer(t)
	go s.refreshLoop()

	for range 2 {
		select {
		case <-hits:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for a periodic catalog refresh")
		}
	}
}
