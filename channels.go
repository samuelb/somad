package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
)

type Playlist struct {
	URL     string `json:"url"`
	Format  string `json:"format"`
	Quality string `json:"quality"`
}

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

type Channels struct {
	Channels []Channel `json:"channels"`
}

const cacheFileName = "somafm_channels.json"

func getCacheFilePath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}
	appCacheDir := filepath.Join(cacheDir, "somacli")
	if err := os.MkdirAll(appCacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create app cache directory: %w", err)
	}
	return filepath.Join(appCacheDir, cacheFileName), nil
}

func readChannelsFromCache() (*Channels, error) {
	cachePath, err := getCacheFilePath()
	if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read cache file: %w", err)
	}

	var channels Channels
	if err := json.Unmarshal(data, &channels); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cached data: %w", err)
	}

	// Optional: Add cache expiration logic here if needed
	// For now, we'll just return the cached data if it exists

	return &channels, nil
}

func writeChannelsToCache(channels *Channels) error {
	cachePath, err := getCacheFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal channels for caching: %w", err)
	}

	if err := ioutil.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write channels to cache file: %w", err)
	}

	return nil
}

func getChannels() (*Channels, error) {
	// Try to read from cache first
	channels, err := readChannelsFromCache()
	if err == nil {
		fmt.Println("Channels loaded from cache.")
		return channels, nil
	}

	// If cache read fails, fetch from network
	fmt.Println("Fetching channels from network...")
	resp, err := http.Get("https://somafm.com/channels.json")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channels from network: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code from network: %d", resp.StatusCode)
	}

	var fetchedChannels Channels
	if err := json.NewDecoder(resp.Body).Decode(&fetchedChannels); err != nil {
		return nil, fmt.Errorf("failed to decode network response: %w", err)
	}

	// Write to cache for future use
	if err := writeChannelsToCache(&fetchedChannels); err != nil {
		fmt.Printf("Warning: Failed to write channels to cache: %v\n", err)
	}

	return &fetchedChannels, nil
}
