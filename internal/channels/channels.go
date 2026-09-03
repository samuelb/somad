package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"somad/internal/atomicfile"
	"somad/internal/security"
	"somad/internal/xdg"
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
	appCacheDirName = "somad"

	// maxCatalogBytes caps the channel-catalog download; the real catalog is
	// a few hundred KB.
	maxCatalogBytes = 4 << 20 // 4 MiB
)

// SomaFMChannelsURL is the URL for fetching channels - exported for testing.
var SomaFMChannelsURL = "https://somafm.com/channels.json"

// cacheFilePath resolves the absolute path of the cache file without
// touching the filesystem.
func cacheFilePath() (string, error) {
	cacheDir, err := xdg.CacheDir(appCacheDirName)
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, cacheFileName), nil
}

// GetCacheFilePath returns the absolute path to the cache file, creating its
// directory so the caller can write to it.
func GetCacheFilePath() (string, error) {
	path, err := cacheFilePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil { // #nosec G703 -- path derived from os.UserCacheDir, not user input
		return "", fmt.Errorf("failed to create app cache directory: %w", err)
	}
	return path, nil
}

// PeekChannelsFromCache reads the cached channel data without side effects:
// no directory is created and a corrupt cache stays in place. Shell
// completion reads through this, since a Tab press must not modify anything.
func PeekChannelsFromCache() (*Channels, error) {
	cachePath, err := cacheFilePath()
	if err != nil {
		return nil, err
	}
	var channels Channels
	if err := atomicfile.ReadJSON(cachePath, &channels); err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}
	return &channels, nil
}

// ReadChannelsFromCache attempts to read channel data from the local cache
// file. A corrupt cache is moved aside (so it does not repeatedly fail
// silently) and reported as an error so the caller falls back to a network
// fetch.
func ReadChannelsFromCache() (*Channels, error) {
	cachePath, err := GetCacheFilePath()
	if err != nil {
		return nil, err
	}
	var channels Channels
	if err := atomicfile.ReadJSON(cachePath, &channels); err != nil {
		if errors.Is(err, atomicfile.ErrCorrupt) {
			atomicfile.Quarantine(cachePath, "channel cache", err)
		}
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}
	return &channels, nil
}

// WriteChannelsToCache writes the given channel data to the local cache file.
func WriteChannelsToCache(channels *Channels) error {
	cachePath, err := GetCacheFilePath()
	if err != nil {
		return err
	}
	// Atomic write: a crash mid-save must not corrupt the cache file.
	if err := atomicfile.WriteJSON(cachePath, channels, 0600); err != nil {
		return fmt.Errorf("failed to write channels to cache file: %w", err)
	}
	return nil
}

// FetchChannelsFromNetwork fetches channel data from the SomaFM API.
func FetchChannelsFromNetwork(userAgent string) (*Channels, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := security.NewRequest(ctx, SomaFMChannelsURL, userAgent)
	if err != nil {
		return nil, fmt.Errorf("invalid channels URL: %w", err)
	}

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channels from network: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from network: %d", resp.StatusCode)
	}

	// The real catalog is well under 1 MB; the cap keeps a misbehaving or
	// compromised upstream from streaming an arbitrarily large body into
	// memory (and from there into the cache file).
	var fetchedChannels Channels
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxCatalogBytes)).Decode(&fetchedChannels); err != nil {
		return nil, fmt.Errorf("failed to decode network response: %w", err)
	}

	// Write to cache for future use
	if err := WriteChannelsToCache(&fetchedChannels); err != nil {
		// Log error but don't fail
		log.Printf("warning: failed to write channels to cache: %v", err)
	}

	return &fetchedChannels, nil
}
