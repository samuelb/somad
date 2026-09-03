package lastfm

import (
	"crypto/md5" // #nosec G501 -- verifying the code under test independently reimplements Last.fm's mandated signature scheme, not a choice made here
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testServer starts an httptest server standing in for Last.fm, points
// APIURL at it for the duration of the test, and allowlists its host so
// security.NewFormRequest accepts it.
func testServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	securitytest.AllowTestHosts(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	prevURL := APIURL
	APIURL = srv.URL + "/"
	t.Cleanup(func() { APIURL = prevURL })
	return srv
}

// signIndependently reimplements the authspec's api_sig algorithm directly
// against crypto/md5, independent of the sign() helper under test, so the
// assertion in assertValidSignature and TestSign_MatchesLastfmAuthspecExample
// isn't just calling the code it is meant to verify.
func signIndependently(params url.Values, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k == "format" || k == "callback" {
			continue
		}
		keys = append(keys, k)
	}
	// A simple insertion sort keeps this independent of the sort package
	// call pattern sign() itself uses, for whatever that's worth; ascending
	// key order is what the authspec requires either way.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	s := ""
	for _, k := range keys {
		s += k + params.Get(k)
	}
	s += secret
	sum := md5.Sum([]byte(s)) // #nosec G401 -- see file header
	return hex.EncodeToString(sum[:])
}

func TestSign_MatchesIndependentReimplementation(t *testing.T) {
	params := url.Values{
		"method":  {"auth.getSession"},
		"api_key": {"b25b959554ed76058ac220b7b2e0a026"},
		"token":   {"d580d57f32848f5dcf574d1c88Fee97"},
		"format":  {"json"}, // must be excluded from the signed string
	}
	secret := "192a83fbaf13b1ded9dbda9b5f7d0eeb" // #nosec G101 -- arbitrary fixture value, not a real credential
	assert.Equal(t, signIndependently(params, secret), sign(params, secret))
}

func TestSign_ExcludesFormatAndCallbackButNotOtherParams(t *testing.T) {
	base := url.Values{"api_key": {"k"}, "method": {"m"}, "token": {"t"}}
	withFormat := cloneValues(base)
	withFormat.Set("format", "json")
	withCallback := cloneValues(base)
	withCallback.Set("callback", "cb")

	sigBase := sign(base, "secret")
	assert.Equal(t, sigBase, sign(withFormat, "secret"), "format must not affect the signature")
	assert.Equal(t, sigBase, sign(withCallback, "secret"), "callback must not affect the signature")

	changed := cloneValues(base)
	changed.Set("token", "different")
	assert.NotEqual(t, sigBase, sign(changed, "secret"), "a real param must affect the signature")
}

func TestClient_GetToken(t *testing.T) {
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "auth.getToken", r.FormValue("method"))
		assert.Equal(t, "testkey", r.FormValue("api_key"))
		assert.Equal(t, "json", r.FormValue("format"))
		assertValidSignature(t, r, "testsecret")
		_, _ = w.Write([]byte(`{"token":"abc123"}`))
	})

	c := New("testkey", "testsecret", "", "soma-test")
	token, err := c.GetToken()
	require.NoError(t, err)
	assert.Equal(t, "abc123", token)
}

func TestClient_AuthURL(t *testing.T) {
	prev := AuthURLBase
	AuthURLBase = "https://www.last.fm/api/auth/"
	t.Cleanup(func() { AuthURLBase = prev })

	c := New("testkey", "testsecret", "", "soma-test")
	got := c.AuthURL("tok123")
	assert.Equal(t, "https://www.last.fm/api/auth/?api_key=testkey&token=tok123", got)
}

func TestClient_GetSession(t *testing.T) {
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "auth.getSession", r.FormValue("method"))
		assert.Equal(t, "tok123", r.FormValue("token"))
		assertValidSignature(t, r, "testsecret")
		_, _ = w.Write([]byte(`{"session":{"name":"someuser","key":"sesskey456","subscriber":0}}`))
	})

	c := New("testkey", "testsecret", "", "soma-test")
	key, err := c.GetSession("tok123")
	require.NoError(t, err)
	assert.Equal(t, "sesskey456", key)
}

func TestClient_UpdateNowPlaying_RequiresSession(t *testing.T) {
	c := New("testkey", "testsecret", "", "soma-test")
	err := c.UpdateNowPlaying("Artist", "Track")
	require.ErrorIs(t, err, ErrNoSession)
}

func TestClient_UpdateNowPlaying_SendsSignedForm(t *testing.T) {
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "track.updateNowPlaying", r.FormValue("method"))
		assert.Equal(t, "Boards of Canada", r.FormValue("artist"))
		assert.Equal(t, "Dayvan Cowboy", r.FormValue("track"))
		assert.Equal(t, "sess-key", r.FormValue("sk"))
		assertValidSignature(t, r, "testsecret")
		_, _ = w.Write([]byte(`{"nowplaying":{}}`))
	})

	c := New("testkey", "testsecret", "sess-key", "soma-test")
	require.NoError(t, c.UpdateNowPlaying("Boards of Canada", "Dayvan Cowboy"))
}

func TestClient_Scrobble_RequiresSession(t *testing.T) {
	c := New("testkey", "testsecret", "", "soma-test")
	err := c.Scrobble("Artist", "Track", time.Now())
	require.ErrorIs(t, err, ErrNoSession)
}

func TestClient_Scrobble_SendsTimestamp(t *testing.T) {
	startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "track.scrobble", r.FormValue("method"))
		assert.Equal(t, "Boards of Canada", r.FormValue("artist"))
		assert.Equal(t, "Dayvan Cowboy", r.FormValue("track"))
		assert.Equal(t, "sess-key", r.FormValue("sk"))
		assert.Equal(t, strconv.FormatInt(startedAt.Unix(), 10), r.FormValue("timestamp"))
		assertValidSignature(t, r, "testsecret")
		_, _ = w.Write([]byte(`{"scrobbles":{}}`))
	})

	c := New("testkey", "testsecret", "sess-key", "soma-test")
	require.NoError(t, c.Scrobble("Boards of Canada", "Dayvan Cowboy", startedAt))
}

func TestClient_SetSessionKey_TakesEffectOnNextCall(t *testing.T) {
	var gotSK string
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotSK = r.FormValue("sk")
		_, _ = w.Write([]byte(`{"nowplaying":{}}`))
	})

	c := New("testkey", "testsecret", "", "soma-test")
	require.ErrorIs(t, c.UpdateNowPlaying("A", "T"), ErrNoSession)

	c.SetSessionKey("fresh-key")
	require.NoError(t, c.UpdateNowPlaying("A", "T"))
	assert.Equal(t, "fresh-key", gotSK)
}

func TestClient_DecodesAPIError(t *testing.T) {
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":9,"message":"Invalid session key"}`))
	})

	c := New("testkey", "testsecret", "sess-key", "soma-test")
	err := c.UpdateNowPlaying("A", "T")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid session key")
	assert.Contains(t, err.Error(), "9")
}

func TestClient_UnexpectedStatusCodeWithoutAPIError(t *testing.T) {
	testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`not json`))
	})

	c := New("testkey", "testsecret", "sess-key", "soma-test")
	err := c.UpdateNowPlaying("A", "T")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestClient_RejectsPlainHTTPHost(t *testing.T) {
	// Without the httptest allowlisting, the real (https-only) Last.fm host
	// check applies: plain http is rejected before any network I/O.
	prevURL := APIURL
	APIURL = "http://ws.audioscrobbler.com/2.0/"
	t.Cleanup(func() { APIURL = prevURL })

	c := New("testkey", "testsecret", "sess-key", "soma-test")
	err := c.UpdateNowPlaying("A", "T")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid last.fm request")
}

// assertValidSignature recomputes api_sig from the request's own form
// values (minus api_sig itself) and checks it matches what was sent, the
// same way Last.fm's real API would.
func assertValidSignature(t *testing.T, r *http.Request, secret string) {
	t.Helper()
	got := r.FormValue("api_sig")
	require.NotEmpty(t, got)
	params := url.Values{}
	for k, v := range r.Form {
		if k == "api_sig" {
			continue
		}
		params[k] = v
	}
	assert.Equal(t, signIndependently(params, secret), got)
}
