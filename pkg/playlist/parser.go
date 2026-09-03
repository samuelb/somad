package playlist

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"somad/internal/security"
)

// maxPlaylistBytes caps how much of a playlist response is read. Playlists
// are a few hundred bytes; the URL can be attacker-influenced via redirects,
// so an unbounded read would be a memory hazard.
const maxPlaylistBytes = 1 << 20 // 1 MiB

// GetStreamURLFromPlaylist fetches a playlist file from a URL, parses it,
// and returns the first stream URL found within the playlist.
// It supports .pls playlist formats.
func GetStreamURLFromPlaylist(playlistURL, userAgent string) (string, error) {
	// Fetch the playlist file content
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := security.NewRequest(ctx, playlistURL, userAgent)
	if err != nil {
		return "", fmt.Errorf("invalid playlist URL: %w", err)
	}

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		return "", fmt.Errorf("failed to get playlist from %s: %w", playlistURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check if the HTTP request was successful
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d for playlist %s", resp.StatusCode, playlistURL)
	}

	url, err := parseFirstStreamURL(io.LimitReader(resp.Body, maxPlaylistBytes))
	if err != nil {
		return "", fmt.Errorf("error reading playlist body from %s: %w", playlistURL, err)
	}
	if url == "" {
		return "", fmt.Errorf("no stream URL found in playlist %s", playlistURL)
	}
	return url, nil
}

// parseFirstStreamURL scans .pls content for FileN entries and returns a
// stream URL, or "" when none is found. It prefers the first https:// entry
// over an earlier http:// (or other-scheme) one, since this is what
// fetchStream connects to and a plain-http entry is vulnerable to MITM of
// both the audio and its ICY titles; with no https entry present it falls
// back to the first entry of any scheme. Real-world playlists are not always
// spec-exact, so keys match case-insensitively and whitespace around keys,
// values, and the "=" is tolerated.
func parseFirstStreamURL(r io.Reader) (string, error) {
	var first, firstHTTPS string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if !isFileKey(strings.TrimSpace(key)) {
			continue
		}
		url := strings.TrimSpace(value)
		if url == "" {
			continue
		}
		if first == "" {
			first = url
		}
		if firstHTTPS == "" && isHTTPSURL(url) {
			firstHTTPS = url
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if firstHTTPS != "" {
		return firstHTTPS, nil
	}
	return first, nil
}

// isHTTPSURL reports whether url starts with an https scheme, matched
// case-insensitively since playlists are not always spec-exact.
func isHTTPSURL(url string) bool {
	const scheme = "https://"
	return len(url) >= len(scheme) && strings.EqualFold(url[:len(scheme)], scheme)
}

// isFileKey reports whether a .pls key names a stream entry: "file" followed
// by digits, in any case.
func isFileKey(key string) bool {
	rest, ok := cutPrefixFold(key, "file")
	if !ok || rest == "" {
		return false
	}
	for _, r := range rest {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cutPrefixFold is strings.CutPrefix with ASCII case-insensitive matching.
func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}
