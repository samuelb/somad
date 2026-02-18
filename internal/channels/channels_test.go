package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"somatui/internal/security"

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
	require.NoError(t, os.MkdirAll(cacheDir, 0755))                                                    // #nosec G301 // Test directory
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, cacheFileName), []byte("not json"), 0644)) // #nosec G306 // Test file

	channels, err := ReadChannelsFromCache()
	assert.Error(t, err)
	assert.Nil(t, channels)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestFetchChannelsFromNetwork(t *testing.T) {
	security.AllowTestHosts(t)
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

	channels, err := FetchChannelsFromNetwork("SomaTUI/test")
	require.NoError(t, err)
	assert.Equal(t, 2, len(channels.Channels))
	assert.Equal(t, "groovesalad", channels.Channels[0].ID)

	// Verify it was also cached
	cached, err := ReadChannelsFromCache()
	require.NoError(t, err)
	assert.Equal(t, len(channels.Channels), len(cached.Channels))
}

func TestFetchChannelsFromNetwork_ServerError(t *testing.T) {
	security.AllowTestHosts(t)
	SetCacheDir(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	originalURL := SomaFMChannelsURL
	SomaFMChannelsURL = server.URL
	t.Cleanup(func() { SomaFMChannelsURL = originalURL })

	channels, err := FetchChannelsFromNetwork("SomaTUI/test")
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

	channels, err := FetchChannelsFromNetwork("SomaTUI/test")
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
