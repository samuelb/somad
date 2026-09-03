package security

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
)

const allowedHostSuffix = ".somafm.com"

// lastfmHosts are the Last.fm API hosts allowed for the scrobbling
// integration (TODO.md "Last.fm scrobbling"). They are a second, explicit
// allowlist rather than a widening of the SomaFM rule above (ADR-0010): a
// bug in the somafm.com check must not accidentally also open the door to
// an unrelated host. Unlike SomaFM's allowlist, these hosts are https-only —
// there is no legacy plain-http use case here, and the API carries session
// keys.
var lastfmHosts = []string{"ws.audioscrobbler.com"}

// maxRedirects matches net/http's default redirect limit, re-applied here
// because supplying CheckRedirect replaces that default.
const maxRedirects = 10

// HTTPClient is the process-wide HTTP client, shared so connections to the
// SomaFM hosts are reused across playlist, channel, stream, and metadata
// requests. Per-request deadlines come from the request context.
//
// CheckRedirect re-validates every redirect target: ValidateURL only guards
// the initial URL, so without this a redirect (feasible over the allowed http
// scheme) could send a request to an internal or otherwise disallowed host.
var HTTPClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		if err := ValidateURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect to disallowed URL: %w", err)
		}
		return nil
	},
}

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

	host := strings.ToLower(parsed.Hostname())

	switch {
	case isSomaFMHost(host), isExtraAllowedHost(host):
		// SomaFM's playlists list plain-http stream URLs, and tests allow
		// their own httptest hosts over http too.
		if parsed.Scheme != "https" && parsed.Scheme != "http" {
			return fmt.Errorf("invalid URL scheme: %s (expected http or https)", parsed.Scheme)
		}
	case slices.Contains(lastfmHosts, host):
		// No legacy plain-http use case for Last.fm, and the API carries
		// session keys, so https is required rather than just preferred.
		if parsed.Scheme != "https" {
			return fmt.Errorf("invalid URL scheme: %s (expected https)", parsed.Scheme)
		}
	default:
		return fmt.Errorf("URL host not allowed: %s (must be somafm.com or subdomain, an explicitly allowed Last.fm host, or a test host)", host)
	}

	return nil
}

// isSomaFMHost reports whether host is somafm.com or one of its
// subdomains (case-insensitive; host must already be lowercased).
func isSomaFMHost(host string) bool {
	return host == "somafm.com" || strings.HasSuffix(host, allowedHostSuffix)
}

func isExtraAllowedHost(host string) bool {
	extraAllowedHostsMu.RLock()
	defer extraAllowedHostsMu.RUnlock()
	return slices.Contains(extraAllowedHosts, host)
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

// NewFormRequest creates a validated HTTP POST request with an
// application/x-www-form-urlencoded body, for APIs (like Last.fm's) that
// take parameters as POST form fields rather than a query string. Returns
// an error if the URL fails host validation or request creation fails.
func NewFormRequest(ctx context.Context, rawURL, userAgent string, form url.Values) (*http.Request, error) {
	if err := ValidateURL(rawURL); err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	return req, nil
}
