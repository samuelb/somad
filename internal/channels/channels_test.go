package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SetCacheDir sets XDG_CACHE_HOME to a temp dir for testing.
func SetCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	return dir
}

var testChannelData = Channels{
	Channels: []Channel{
		{
			ID:          "groovesalad",
			Title:       "Groove Salad",
			Description: "A nicely chilled plate of ambient/downtempo beats and grooves.",
			Genre:       "ambient|electronica|chillout",
			Listeners:   "1234",
			Playlists: []Playlist{
				{URL: "http://somafm.com/groovesalad130.pls", Format: "mp3", Quality: "high"},
			},
		},
		{
			ID:          "dronezone",
			Title:       "Drone Zone",
			Description: "Served best chilled, safe with most medications.",
			Genre:       "ambient|space",
			Listeners:   "567",
			Playlists: []Playlist{
				{URL: "http://somafm.com/dronezone130.pls", Format: "mp3", Quality: "high"},
				{URL: "http://somafm.com/dronezone64.pls", Format: "aac", Quality: "low"},
			},
		},
	},
}

func TestWriteAndReadChannelsFromCache(t *testing.T) {
	SetCacheDir(t)

	err := WriteChannelsToCache(&testChannelData)
	require.NoError(t, err)

	loaded, err := ReadChannelsFromCache()
	require.NoError(t, err)
	assert.Equal(t, len(testChannelData.Channels), len(loaded.Channels))
	assert.Equal(t, "groovesalad", loaded.Channels[0].ID)
	assert.Equal(t, "Groove Salad", loaded.Channels[0].Title)
	assert.Equal(t, "dronezone", loaded.Channels[1].ID)
	assert.Len(t, loaded.Channels[1].Playlists, 2)
}

func TestReadChannelsFromCache_NoFile(t *testing.T) {
	SetCacheDir(t)

	channels, err := ReadChannelsFromCache()
	assert.Error(t, err)
	assert.Nil(t, channels)
}

func TestReadChannelsFromCache_CorruptJSON(t *testing.T) {
	dir := SetCacheDir(t)

	cacheDir := filepath.Join(dir, appCacheDirName)
	cachePath := filepath.Join(cacheDir, cacheFileName)
	require.NoError(t, os.MkdirAll(cacheDir, 0755))                       // #nosec G301 // Test directory
	require.NoError(t, os.WriteFile(cachePath, []byte("not json"), 0644)) // #nosec G306 // Test file

	channels, err := ReadChannelsFromCache()
	assert.Error(t, err)
	assert.Nil(t, channels)
	assert.Contains(t, err.Error(), "unmarshal")

	// A corrupt cache must not repeatedly fail silently: it is moved aside
	// for inspection, like a corrupt state.json.
	assert.NoFileExists(t, cachePath)
	backup, err := os.ReadFile(cachePath + ".corrupt") // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "not json", string(backup))
}

func TestPeekChannelsFromCache(t *testing.T) {
	SetCacheDir(t)

	require.NoError(t, WriteChannelsToCache(&testChannelData))
	loaded, err := PeekChannelsFromCache()
	require.NoError(t, err)
	assert.Equal(t, "groovesalad", loaded.Channels[0].ID)
}

// TestPeekChannelsFromCache_NoSideEffects pins the read-only contract shell
// completion relies on: a Tab press must not create the cache directory or
// move a corrupt cache aside.
func TestPeekChannelsFromCache_NoSideEffects(t *testing.T) {
	dir := SetCacheDir(t)
	cacheDir := filepath.Join(dir, appCacheDirName)
	cachePath := filepath.Join(cacheDir, cacheFileName)

	channels, err := PeekChannelsFromCache()
	assert.Error(t, err)
	assert.Nil(t, channels)
	assert.NoDirExists(t, cacheDir)

	require.NoError(t, os.MkdirAll(cacheDir, 0755))                       // #nosec G301 // Test directory
	require.NoError(t, os.WriteFile(cachePath, []byte("not json"), 0644)) // #nosec G306 // Test file
	channels, err = PeekChannelsFromCache()
	assert.Error(t, err)
	assert.Nil(t, channels)
	assert.FileExists(t, cachePath) // still in place, unlike ReadChannelsFromCache
	assert.NoFileExists(t, cachePath+".corrupt")
}

func TestFetchChannelsFromNetwork(t *testing.T) {
	securitytest.AllowTestHosts(t)
	SetCacheDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data, _ := json.Marshal(testChannelData)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	// Override the URL for testing
	originalURL := SomaFMChannelsURL
	SomaFMChannelsURL = server.URL
	t.Cleanup(func() { SomaFMChannelsURL = originalURL })

	channels, err := FetchChannelsFromNetwork("soma/test")
	require.NoError(t, err)
	assert.Equal(t, 2, len(channels.Channels))
	assert.Equal(t, "groovesalad", channels.Channels[0].ID)

	// Verify it was also cached
	cached, err := ReadChannelsFromCache()
	require.NoError(t, err)
	assert.Equal(t, len(channels.Channels), len(cached.Channels))
}

func TestFetchChannelsFromNetwork_ServerError(t *testing.T) {
	securitytest.AllowTestHosts(t)
	SetCacheDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	originalURL := SomaFMChannelsURL
	SomaFMChannelsURL = server.URL
	t.Cleanup(func() { SomaFMChannelsURL = originalURL })

	channels, err := FetchChannelsFromNetwork("soma/test")
	assert.Error(t, err)
	assert.Nil(t, channels)
	assert.Contains(t, err.Error(), "500")
}

func TestFetchChannelsFromNetwork_InvalidJSON(t *testing.T) {
	SetCacheDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	originalURL := SomaFMChannelsURL
	SomaFMChannelsURL = server.URL
	t.Cleanup(func() { SomaFMChannelsURL = originalURL })

	channels, err := FetchChannelsFromNetwork("soma/test")
	assert.Error(t, err)
	assert.Nil(t, channels)
}

func TestFetchChannelsFromNetwork_OversizedBodyRejected(t *testing.T) {
	securitytest.AllowTestHosts(t)
	SetCacheDir(t)

	// A body larger than the catalog cap must fail the decode instead of
	// being streamed into memory whole.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"channels":[{"id":"x","title":"`))
		filler := strings.Repeat("a", 64<<10)
		for written := 0; written < maxCatalogBytes+(64<<10); written += len(filler) {
			if _, err := w.Write([]byte(filler)); err != nil {
				return
			}
		}
		_, _ = w.Write([]byte(`"}]}`))
	}))
	defer server.Close()

	originalURL := SomaFMChannelsURL
	SomaFMChannelsURL = server.URL
	t.Cleanup(func() { SomaFMChannelsURL = originalURL })

	channels, err := FetchChannelsFromNetwork("soma/test")
	assert.Error(t, err)
	assert.Nil(t, channels)
}

func TestGetCacheFilePath(t *testing.T) {
	SetCacheDir(t)

	path, err := GetCacheFilePath()
	require.NoError(t, err)
	assert.Contains(t, path, appCacheDirName)
	assert.Contains(t, path, cacheFileName)
}
