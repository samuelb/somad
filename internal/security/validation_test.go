package security

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid somafm URL",
			url:     "https://somafm.com/channels.json",
			wantErr: false,
		},
		{
			name:    "valid somafm subdomain",
			url:     "https://ice1.somafm.com/stream",
			wantErr: false,
		},
		{
			name:    "valid http somafm URL",
			url:     "http://somafm.com/stream",
			wantErr: false,
		},
		{
			name:    "invalid external domain",
			url:     "https://evil.com/stream",
			wantErr: true,
		},
		{
			name:    "invalid scheme",
			url:     "ftp://somafm.com/file",
			wantErr: true,
		},
		{
			name:    "malformed URL",
			url:     "://invalid",
			wantErr: true,
		},
		{
			name:    "subdomain that looks like somafm",
			url:     "https://somafm.com.evil.com/stream",
			wantErr: true,
		},
		{
			name:    "host ending in somafm.com without a dot boundary",
			url:     "https://evilsomafm.com/stream",
			wantErr: true,
		},
		{
			name:    "mixed-case somafm subdomain",
			url:     "https://Ice1.SomaFM.Com/stream",
			wantErr: false,
		},
		{
			name:    "empty host",
			url:     "https:///channels.json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateURLExtraAllowedHost(t *testing.T) {
	const host = "localhost"
	url := "http://" + host + ":8080/stream"

	require.Error(t, ValidateURL(url), "host should be rejected before it is allowed")

	AddAllowedHost(host)
	defer ClearAllowedHosts()
	require.NoError(t, ValidateURL(url), "host should be accepted once allowed")

	ClearAllowedHosts()
	require.Error(t, ValidateURL(url), "host should be rejected again after clearing")
}

func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Hostname()
}

func TestHTTPClientRedirectValidation(t *testing.T) {
	t.Run("rejects redirect to a disallowed host", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// CheckRedirect must reject this before any request reaches the
			// disallowed host, so the target need not exist.
			http.Redirect(w, r, "http://disallowed.example.com/stream", http.StatusFound)
		}))
		defer srv.Close()

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := HTTPClient.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disallowed URL")
	})

	t.Run("allows a redirect that stays on an allowed host", func(t *testing.T) {
		mux := http.NewServeMux()
		srv := httptest.NewServer(mux)
		defer srv.Close()

		// The httptest host (127.0.0.1) is not a SomaFM host; whitelist it so
		// the redirect target passes validation for the duration of the test.
		AddAllowedHost(hostOf(t, srv.URL))
		defer ClearAllowedHosts()

		var endHits int
		mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, srv.URL+"/end", http.StatusFound)
		})
		mux.HandleFunc("/end", func(w http.ResponseWriter, r *http.Request) {
			endHits++
			w.WriteHeader(http.StatusOK)
		})

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/start", nil)
		require.NoError(t, err)
		resp, err := HTTPClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 1, endHits)
	})
}
