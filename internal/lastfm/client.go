// Package lastfm implements enough of the Last.fm API 2.0
// (https://www.last.fm/api) for the desktop auth flow, now-playing
// updates, and scrobbling (TODO.md "Last.fm scrobbling"): auth.getToken,
// auth.getSession, track.updateNowPlaying, and track.scrobble. Every
// request goes through security.NewFormRequest / security.HTTPClient, so
// it is bound by the same host allowlist as every other outbound call this
// daemon makes (internal/security, ADR-0010): only https://ws.audioscrobbler.com
// is reachable.
package lastfm

import (
	"context"
	"crypto/md5" // #nosec G501 -- mandated by Last.fm's API signature scheme (https://www.last.fm/api/authspec), not a choice made here
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"somad/internal/security"
)

// APIURL is the Last.fm API 2.0 endpoint. A variable so tests can point it
// at an httptest server (allowlisted via internal/security/securitytest).
var APIURL = "https://ws.audioscrobbler.com/2.0/"

// AuthURLBase is where a user authorizes a token in a browser (step 2 of
// https://www.last.fm/api/desktopauth). A variable so tests can override it.
var AuthURLBase = "https://www.last.fm/api/auth/"

// requestTimeout bounds every API call, so a slow or unreachable Last.fm
// never blocks its caller for long — the playback hot path calls these off
// a goroutine (see internal/server's scrobble pipeline). A variable so
// tests can shrink it.
var requestTimeout = 10 * time.Second

// maxResponseBytes caps a decoded response body, mirroring the caps this
// codebase applies to every other outbound response (ADR-0010).
const maxResponseBytes = 1 << 20 // 1 MiB

// ErrNoSession is returned by UpdateNowPlaying and Scrobble when no session
// key is configured yet (before "soma lastfm login").
var ErrNoSession = errors.New(`lastfm: no session key configured; run "soma lastfm login"`)

// Client is a Last.fm API 2.0 client for the desktop auth flow, now-playing
// updates, and scrobbling. Safe for concurrent use.
type Client struct {
	apiKey    string
	apiSecret string
	userAgent string

	mu         sync.RWMutex
	sessionKey string
}

// New returns a Client for the given API key/secret (create a pair at
// https://www.last.fm/api/account/create) and an optional initial session
// key (empty until a successful login).
func New(apiKey, apiSecret, sessionKey, userAgent string) *Client {
	return &Client{apiKey: apiKey, apiSecret: apiSecret, sessionKey: sessionKey, userAgent: userAgent}
}

// SetSessionKey updates the session key calls authenticate with, without
// reconstructing the Client — used to apply a session obtained by "soma
// lastfm login" after this Client (and the daemon holding it) was already
// running, see server.Server.ReloadLastfm.
func (c *Client) SetSessionKey(key string) {
	c.mu.Lock()
	c.sessionKey = key
	c.mu.Unlock()
}

func (c *Client) session() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionKey
}

// apiError is Last.fm's error response shape, present alongside a non-2xx
// (usually 200 even for API-level failures) response:
// {"error": <code>, "message": "..."}.
type apiError struct {
	Error   int    `json:"error"`
	Message string `json:"message"`
}

func (e apiError) asError() error {
	if e.Error == 0 {
		return nil
	}
	return fmt.Errorf("last.fm API error %d: %s", e.Error, e.Message)
}

// sign computes the api_sig every Last.fm write call requires: the MD5 hex
// digest of every parameter's name and value concatenated in ascending key
// order (excluding "format" and "callback", which the signature spec
// excludes), with the shared secret appended.
// https://www.last.fm/api/authspec#8
func sign(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(params.Get(k))
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String())) // #nosec G401 -- mandated by Last.fm's API signature scheme, not a choice made here
	return hex.EncodeToString(sum[:])
}

// call POSTs a signed request for method with the given params (which must
// not set method, api_key, format, or api_sig — call adds those) and
// decodes the JSON result into out (ignored when nil).
func (c *Client) call(method string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	} else {
		// Callers pass a fresh url.Values per call, but clone defensively so
		// a caller that reuses one is never surprised by mutation.
		params = cloneValues(params)
	}
	params.Set("method", method)
	params.Set("api_key", c.apiKey)
	params.Set("format", "json")
	params.Set("api_sig", sign(params, c.apiSecret))

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req, err := security.NewFormRequest(ctx, APIURL, c.userAgent, params)
	if err != nil {
		return fmt.Errorf("invalid last.fm request: %w", err)
	}

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewFormRequest()
	if err != nil {
		return fmt.Errorf("last.fm %s request failed: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("reading last.fm %s response: %w", method, err)
	}

	var apiErr apiError
	if err := json.Unmarshal(body, &apiErr); err == nil {
		if err := apiErr.asError(); err != nil {
			return err
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code from last.fm %s: %d", method, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decoding last.fm %s response: %w", method, err)
		}
	}
	return nil
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		out[k] = append([]string(nil), vals...)
	}
	return out
}

// GetToken requests a fresh, unauthorized token (step 1 of the desktop auth
// flow, https://www.last.fm/api/desktopauth).
func (c *Client) GetToken() (string, error) {
	var result struct {
		Token string `json:"token"`
	}
	if err := c.call("auth.getToken", nil, &result); err != nil {
		return "", err
	}
	return result.Token, nil
}

// AuthURL is the URL to send the user to authorize token (step 2 of the
// desktop auth flow).
func (c *Client) AuthURL(token string) string {
	v := url.Values{"api_key": {c.apiKey}, "token": {token}}
	return AuthURLBase + "?" + v.Encode()
}

// GetSession exchanges a user-authorized token for a permanent session key
// (step 3 of the desktop auth flow). The caller persists the result (see
// internal/state's LastfmSession) and typically calls SetSessionKey with it.
func (c *Client) GetSession(token string) (string, error) {
	var result struct {
		Session struct {
			Key string `json:"key"`
		} `json:"session"`
	}
	if err := c.call("auth.getSession", url.Values{"token": {token}}, &result); err != nil {
		return "", err
	}
	return result.Session.Key, nil
}

// UpdateNowPlaying tells Last.fm what is currently playing. It requires a
// session key (a prior successful login / SetSessionKey).
func (c *Client) UpdateNowPlaying(artist, track string) error {
	sk := c.session()
	if sk == "" {
		return ErrNoSession
	}
	params := url.Values{"artist": {artist}, "track": {track}, "sk": {sk}}
	return c.call("track.updateNowPlaying", params, nil)
}

// Scrobble records a completed track play that started at startedAt. It
// requires a session key (a prior successful login / SetSessionKey).
func (c *Client) Scrobble(artist, track string, startedAt time.Time) error {
	sk := c.session()
	if sk == "" {
		return ErrNoSession
	}
	params := url.Values{
		"artist":    {artist},
		"track":     {track},
		"timestamp": {strconv.FormatInt(startedAt.Unix(), 10)},
		"sk":        {sk},
	}
	return c.call("track.scrobble", params, nil)
}
