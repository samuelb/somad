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

// GetStreamURLsFromPlaylist fetches a playlist file from a URL, parses it,
// and returns every stream URL it lists, most preferred first (see
// parseStreamURLs). SomaFM playlists list the same stream on several mirror
// hosts, so the caller can move on to the next one when a host is down.
// It supports .pls playlist formats.
func GetStreamURLsFromPlaylist(playlistURL, userAgent string) ([]string, error) {
	// Fetch the playlist file content
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := security.NewRequest(ctx, playlistURL, userAgent)
	if err != nil {
		return nil, fmt.Errorf("invalid playlist URL: %w", err)
	}

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist from %s: %w", playlistURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check if the HTTP request was successful
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d for playlist %s", resp.StatusCode, playlistURL)
	}

	urls, err := parseStreamURLs(io.LimitReader(resp.Body, maxPlaylistBytes))
	if err != nil {
		return nil, fmt.Errorf("error reading playlist body from %s: %w", playlistURL, err)
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("no stream URL found in playlist %s", playlistURL)
	}
	return urls, nil
}

// parseStreamURLs scans .pls content for FileN entries and returns their
// stream URLs without duplicates (none when there are none). https://
// entries come before http:// (and other-scheme) ones, since the caller
// connects to them in this order and a plain-http entry is vulnerable to
// MITM of both the audio and its ICY titles; within each group the
// playlist's own order is kept. Real-world playlists are not always
// spec-exact, so keys match case-insensitively and whitespace around keys,
// values, and the "=" is tolerated.
func parseStreamURLs(r io.Reader) ([]string, error) {
	var https, other []string
	seen := make(map[string]bool)
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
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		if security.IsHTTPSURL(url) {
			https = append(https, url)
		} else {
			other = append(other, url)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return append(https, other...), nil
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
