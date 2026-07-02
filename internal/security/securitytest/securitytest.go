// Package securitytest provides test helpers for relaxing the security
// package's host allowlist. It lives outside the security package so the
// production binary never links against the testing package.
package securitytest

import (
	"testing"

	"somatui/internal/security"
)

// AllowTestHosts allows requests to localhost test servers for the duration of t.
func AllowTestHosts(t *testing.T) {
	t.Helper()
	security.AddAllowedHost("127.0.0.1")
	security.AddAllowedHost("localhost")
	t.Cleanup(security.ClearAllowedHosts)
}
