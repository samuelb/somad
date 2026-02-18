package security

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

const allowedHostSuffix = ".somafm.com"

var extraAllowedHosts []string

func AddAllowedHost(host string) {
	extraAllowedHosts = append(extraAllowedHosts, host)
}

func ClearAllowedHosts() {
	extraAllowedHosts = nil
}

func AllowTestHosts(t *testing.T) {
	t.Helper()
	AddAllowedHost("127.0.0.1")
	AddAllowedHost("localhost")
	t.Cleanup(ClearAllowedHosts)
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
