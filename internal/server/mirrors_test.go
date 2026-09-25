package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/security/securitytest"
	"somad/pkg/playlist"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withMirrors makes every playlist resolve to n mirror URLs, in order:
// "<playlist>#m1", "<playlist>#m2", ...
func withMirrors(t *testing.T, n int) {
	t.Helper()
	prev := resolveStreamURLs
	resolveStreamURLs = func(playlistURL, _ string) ([]string, error) {
		urls := make([]string, n)
		for i := range urls {
			urls[i] = fmt.Sprintf("%s#m%d", playlistURL, i+1)
		}
		return urls, nil
	}
	t.Cleanup(func() { resolveStreamURLs = prev })
}

func (p *mockPlayer) attemptedURLs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.attempts...)
}

func TestPlay_FallsBackToNextMirrorBeforeNextFormat(t *testing.T) {
	s, player := newTestServer(t, Config{})
	supportedFormats = func() []string { return []string{audio.FormatAAC, audio.FormatMP3} }
	withMirrors(t, 3)
	player.mu.Lock()
	// The first host is down: both formats' first mirror sits on it.
	player.failURLs = map[string]bool{
		"http://somafm.com/both130.pls#m1": true,
		"http://somafm.com/both.pls#m1":    true,
	}
	player.mu.Unlock()

	c := connect(t, s)
	c.hello()
	st := decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"}))

	// The preferred format survives a dead host.
	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, []string{"http://somafm.com/both130.pls#m1", "http://somafm.com/both130.pls#m2"}, player.attemptedURLs())
	player.mu.Lock()
	assert.Equal(t, []string{audio.FormatAAC}, player.playFormats)
	player.mu.Unlock()
}

func TestPlay_TriesEveryMirrorThenNextFormat(t *testing.T) {
	s, player := newTestServer(t, Config{})
	supportedFormats = func() []string { return []string{audio.FormatAAC, audio.FormatMP3} }
	withMirrors(t, 3)
	player.mu.Lock()
	player.failFormats = map[string]bool{audio.FormatAAC: true}
	player.mu.Unlock()

	c := connect(t, s)
	c.hello()
	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"}))

	assert.Equal(t, []string{
		"http://somafm.com/both130.pls#m1",
		"http://somafm.com/both130.pls#m2",
		"http://somafm.com/both130.pls#m3",
		"http://somafm.com/both.pls#m1",
	}, player.attemptedURLs())
}

func TestPlay_MirrorsPerFormatAreCapped(t *testing.T) {
	s, player := newTestServer(t, Config{})
	withMirrors(t, maxStreamMirrors+2)
	player.setPlayErr(errors.New("connection refused"))

	c := connect(t, s)
	c.hello()
	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"})

	// However long the playlist, one attempt's worst case stays bounded.
	assert.Contains(t, resp.Error, "connection refused")
	assert.Len(t, player.attemptedURLs(), maxStreamMirrors)
}

func TestPlay_AudioDeviceFailureStopsTryingStreams(t *testing.T) {
	s, player := newTestServer(t, Config{})
	supportedFormats = func() []string { return []string{audio.FormatAAC, audio.FormatMP3} }
	withMirrors(t, 3)
	player.setPlayErr(fmt.Errorf("audio device not ready: %w", audio.ErrAudioDevice))

	c := connect(t, s)
	c.hello()
	resp := c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "bothformats"})

	// Another stream would only wait for the device again.
	assert.Contains(t, resp.Error, "audio device not ready")
	assert.Len(t, player.attemptedURLs(), 1)
	assert.Equal(t, protocol.StatusReconnecting, s.Snapshot().Status)
}

func TestReconnect_StartsOnTheNextMirror(t *testing.T) {
	prev := reconnectBaseDelay
	reconnectBaseDelay = time.Millisecond
	defer func() { reconnectBaseDelay = prev }()

	s, player := newTestServer(t, Config{})
	withMirrors(t, 3)
	go s.watchPlayerErrors()
	c := connect(t, s)
	c.hello()

	decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "dronezone"}))
	player.errChan <- errors.New("stream read error")
	c.waitState("reconnecting", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusReconnecting
	})
	c.waitState("recovered", func(st protocol.PlaybackState) bool {
		return st.Status == protocol.StatusPlaying
	})

	// The host that just dropped the stream is not the first one retried.
	assert.Equal(t, []string{
		"http://somafm.com/dronezone.pls#m1",
		"http://somafm.com/dronezone.pls#m2",
	}, player.attemptedURLs())
}

func TestPlay_MirrorFailoverOverHTTP(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// Three mirrors behind a real playlist: one host refuses connections,
	// one answers 503, and one serves the stream.
	refused := httptest.NewServer(http.NotFoundHandler())
	refused.Close()
	unavailable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(unavailable.Close)
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("audio"))
	}))
	t.Cleanup(healthy.Close)
	pls := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "[playlist]\nNumberOfEntries=3\nFile1=%s/s\nFile2=%s/s\nFile3=%s/s\n",
			refused.URL, unavailable.URL, healthy.URL)
	}))
	t.Cleanup(pls.Close)

	s, player := newTestServer(t, Config{})
	resolveStreamURLs = playlist.GetStreamURLsFromPlaylist
	s.setCatalog(append(testChannels(), channels.Channel{
		ID:        "mirrored",
		Title:     "Mirrored",
		Playlists: []channels.Playlist{{URL: pls.URL, Format: "mp3", Quality: "highest"}},
	}))
	player.mu.Lock()
	player.dial = func(url string) error {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req) // #nosec G704 -- test servers only
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
		return nil
	}
	player.mu.Unlock()

	c := connect(t, s)
	c.hello()
	st := decodeState(t, c.call(protocol.MethodPlay, protocol.PlayParams{ChannelID: "mirrored"}))

	assert.Equal(t, protocol.StatusPlaying, st.Status)
	attempts := player.attemptedURLs()
	require.Len(t, attempts, 3)
	assert.True(t, strings.HasPrefix(attempts[2], healthy.URL), "played %v", attempts)
}
