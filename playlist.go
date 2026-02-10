package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const plsFilePrefix = "File1="

// getStreamURLFromPlaylist fetches a playlist file from a URL, parses it,
// and returns the first stream URL found within the playlist.
// It supports .pls playlist formats.
func getStreamURLFromPlaylist(playlistURL string) (string, error) {
	// Fetch the playlist file content
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "GET", playlistURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get playlist from %s: %w", playlistURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check if the HTTP request was successful
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d for playlist %s", resp.StatusCode, playlistURL)
	}

	// Scan the playlist file line by line to find the stream URL
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// In .pls files, the stream URL is typically on a line starting with "File1="
		if strings.HasPrefix(line, plsFilePrefix) {
			return strings.TrimPrefix(line, plsFilePrefix), nil
		}
	}

	// Check for any errors during scanning
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading playlist body from %s: %w", playlistURL, err)
	}

	// If no stream URL was found in the playlist
	return "", fmt.Errorf("no stream URL found in playlist %s", playlistURL)
}
