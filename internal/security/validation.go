package security

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const allowedHostSuffix = ".somafm.com"

// extraAllowedHostsMu guards extraAllowedHosts. ValidateURL reads this state
// from any goroutine that makes a request (metadata, player, channel fetch),
// while the test helpers below mutate it, so access must be synchronized.
var (
	extraAllowedHostsMu sync.RWMutex
	extraAllowedHosts   []string
)

func AddAllowedHost(host string) {
	extraAllowedHostsMu.Lock()
	defer extraAllowedHostsMu.Unlock()
	extraAllowedHosts = append(extraAllowedHosts, host)
}

func ClearAllowedHosts() {
	extraAllowedHostsMu.Lock()
	defer extraAllowedHostsMu.Unlock()
	extraAllowedHosts = nil
}

func ValidateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("invalid URL scheme: %s (expected http or https)", parsed.Scheme)
	}

	host := parsed.Hostname()
	if !strings.HasSuffix(host, allowedHostSuffix) && host != "somafm.com" && !isExtraAllowedHost(host) {
		return fmt.Errorf("URL host not allowed: %s (must be somafm.com or subdomain)", host)
	}

	return nil
}

func isExtraAllowedHost(host string) bool {
	extraAllowedHostsMu.RLock()
	defer extraAllowedHostsMu.RUnlock()
	for _, h := range extraAllowedHosts {
		if h == host {
			return true
		}
	}
	return false
}

func ValidatePathNoTraversal(path string) error {
	if strings.Contains(path, "..") {
		return fmt.Errorf("path contains traversal sequence")
	}
	return nil
}

// NewRequest creates a validated HTTP GET request with the given context, URL, and
// User-Agent. Returns an error if the URL fails host validation or request creation fails.
// Callers may add additional headers to the returned request before use.
func NewRequest(ctx context.Context, rawURL, userAgent string) (*http.Request, error) {
	if err := ValidateURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	return req, nil
}
