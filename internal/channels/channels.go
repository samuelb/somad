package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Playlist represents a single playlist entry for a SomaFM channel.
type Playlist struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	Quality string `json:"quality"`
}

// Channel represents a single SomaFM radio channel.
type Channel struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Genre       string     `json:"genre"`
	Image       string     `json:"image"`
	LargeImage  string     `json:"largeimage"`
	XLImage     string     `json:"xlimage"`
	Twitter     string     `json:"twitter"`
	Listeners   string     `json:"listeners"`
	LastPlaying string     `json:"lastPlaying"`
	Playlists   []Playlist `json:"playlists"`
}

// Channels is a wrapper for the list of SomaFM channels.
type Channels struct {
	Channels []Channel `json:"channels"`
}

const (
	cacheFileName   = "somafm_channels.json"
	appCacheDirName = "somatui"
)

// SomaFMChannelsURL is the URL for fetching channels - exported for testing.
var SomaFMChannelsURL = "https://somafm.com/channels.json"

// GetCacheFilePath returns the absolute path to the cache file.
func GetCacheFilePath() (string, error) {
	// Check XDG override first (works on all platforms, enables testing)
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		var err error
		cacheDir, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user cache directory: %w", err)
		}
	}
	appCacheDir := filepath.Join(cacheDir, appCacheDirName)
	if err := os.MkdirAll(appCacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create app cache directory: %w", err)
	}
	return filepath.Join(appCacheDir, cacheFileName), nil
}

// ReadChannelsFromCache attempts to read channel data from the local cache file.
func ReadChannelsFromCache() (*Channels, error) {
	cachePath, err := GetCacheFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var channels Channels
	if err := json.Unmarshal(data, &channels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cached data: %w", err)
	}

	return &channels, nil
}

// WriteChannelsToCache writes the given channel data to the local cache file.
func WriteChannelsToCache(channels *Channels) error {
	cachePath, err := GetCacheFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal channels for caching: %w", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write channels to cache file: %w", err)
	}

	return nil
}

// FetchChannelsFromNetwork fetches channel data from the SomaFM API.
func FetchChannelsFromNetwork(userAgent string) (*Channels, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "GET", SomaFMChannelsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channels from network: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from network: %d", resp.StatusCode)
	}

	var fetchedChannels Channels
	if err := json.NewDecoder(resp.Body).Decode(&fetchedChannels); err != nil {
		return nil, fmt.Errorf("failed to decode network response: %w", err)
	}

	// Write to cache for future use
	if err := WriteChannelsToCache(&fetchedChannels); err != nil {
		// Log error but don't fail
		fmt.Fprintf(os.Stderr, "Warning: Failed to write channels to cache: %v\n", err)
	}

	return &fetchedChannels, nil
}
