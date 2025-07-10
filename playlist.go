package main

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
)

// getStreamURLFromPlaylist fetches a playlist file from a URL, parses it,
// and returns the first stream URL found.
func getStreamURLFromPlaylist(playlistURL string) (string, error) {
	resp, err := http.Get(playlistURL)
	if err != nil {
		return "", fmt.Errorf("failed to get playlist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code for playlist: %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		// .pls files have stream URLs in FileN=... format
		if strings.HasPrefix(line, "File1=") {
			return strings.TrimPrefix(line, "File1="), nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading playlist body: %w", err)
	}

	return "", fmt.Errorf("no stream URL found in playlist")
}
