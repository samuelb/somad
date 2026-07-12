package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"somad/internal/channels"
	"somad/internal/client"
	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemon is a scripted playback daemon for exercising the CLI commands
// end-to-end over the wire protocol, without audio or spawning.
type fakeDaemon struct {
	mu        sync.Mutex
	plays     []string
	deltas    []int
	stops     int
	shutdowns int
	status    protocol.PlaybackState
	payload   protocol.ChannelsPayload
}

func startFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	catalog := []channels.Channel{
		{ID: "groovesalad", Title: "Groove Salad", Genre: "ambient"},
		{ID: "dronezone", Title: "Drone Zone", Genre: "ambient|space"},
	}
	d := &fakeDaemon{
		status: protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 0.5},
		payload: protocol.ChannelsPayload{
			Channels:      catalog,
			Favorites:     []string{"dronezone"},
			LastChannelID: "groovesalad",
		},
	}

	path := filepath.Join(shortTempDir(t), "d.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go d.serve(nc)
		}
	}()

	setEndpoint(t, client.UnixEndpoint(path))
	return d
}

func (d *fakeDaemon) serve(nc net.Conn) {
	defer func() { _ = nc.Close() }()
	sc := protocol.NewScanner(nc)
	for sc.Scan() {
		var req protocol.Request
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		result := d.handle(req)
		if result == nil {
			continue
		}
		raw, _ := json.Marshal(result)
		_ = protocol.WriteLine(nc, protocol.Response{ID: req.ID, Result: raw})
	}
}

// handle mutates the daemon state like the real server would, coarsely.
func (d *fakeDaemon) handle(req protocol.Request) any {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch req.Method {
	case protocol.MethodHello:
		return protocol.HelloResult{ServerVersion: version, ProtocolVersion: protocol.Version}
	case protocol.MethodStatus:
		return d.status
	case protocol.MethodChannels:
		return d.payload
	case protocol.MethodPlay:
		var p protocol.PlayParams
		_ = json.Unmarshal(req.Params, &p)
		d.plays = append(d.plays, p.ChannelID)
		d.setPlayingLocked(p.ChannelID)
		return d.status
	case protocol.MethodPlayRelative:
		var p protocol.PlayRelativeParams
		_ = json.Unmarshal(req.Params, &p)
		d.deltas = append(d.deltas, p.Delta)
		d.setPlayingLocked(d.payload.Channels[0].ID)
		return d.status
	case protocol.MethodPlayPause:
		if d.status.Status == protocol.StatusStopped {
			d.setPlayingLocked(d.payload.LastChannelID)
		} else {
			d.status = protocol.PlaybackState{Status: protocol.StatusStopped, Volume: d.status.Volume}
		}
		return d.status
	case protocol.MethodStop:
		d.stops++
		d.status = protocol.PlaybackState{Status: protocol.StatusStopped, Volume: d.status.Volume}
		return d.status
	case protocol.MethodSetVolume:
		var p protocol.SetVolumeParams
		_ = json.Unmarshal(req.Params, &p)
		d.status.Volume = p.Volume
		return d.status
	case protocol.MethodToggleFavorite:
		var p protocol.ToggleFavoriteParams
		_ = json.Unmarshal(req.Params, &p)
		if i := slices.Index(d.payload.Favorites, p.ChannelID); i >= 0 {
			d.payload.Favorites = slices.Delete(slices.Clone(d.payload.Favorites), i, i+1)
		} else {
			d.payload.Favorites = append(slices.Clone(d.payload.Favorites), p.ChannelID)
		}
		return protocol.FavoritesResult{Favorites: d.payload.Favorites}
	case protocol.MethodShutdown:
		d.shutdowns++
		return struct{}{}
	}
	return nil
}

func (d *fakeDaemon) setPlayingLocked(id string) {
	title := id
	for _, ch := range d.payload.Channels {
		if ch.ID == id {
			title = ch.Title
		}
	}
	d.status = protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: id, ChannelTitle: title, Volume: d.status.Volume,
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	prev := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = prev }()
	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestRunPlay_ResolvesAndPlays(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runPlay([]string{"groove"}) })

	assert.Contains(t, out, "Playing: Groove Salad")
	assert.Equal(t, []string{"groovesalad"}, d.plays, "the substring must resolve to the channel ID")
}

func TestRunPlay_NoArgResumesLastChannel(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runPlay(nil) })

	assert.Contains(t, out, "Playing: Groove Salad")
	assert.Equal(t, []string{"groovesalad"}, d.plays)
}

func TestRunList_PlainAndJSON(t *testing.T) {
	startFakeDaemon(t)

	plain := captureStdout(t, func() { runList(nil) })
	assert.Contains(t, plain, "groovesalad")
	assert.Contains(t, plain, "* dronezone", "favorites carry the star marker")

	jsonOut := captureStdout(t, func() { runList([]string{"--json"}) })
	var entries []channelListEntry
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &entries))
	require.Len(t, entries, 2)
	assert.True(t, entries[1].Favorite, "dronezone is a favorite")
}

func TestRunStop_StopsPlayback(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runStop() })

	assert.Contains(t, out, "Stopped")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.stops)
}

func TestRunPause_TogglesBothWays(t *testing.T) {
	startFakeDaemon(t)

	out := captureStdout(t, func() { runPause() })
	assert.Contains(t, out, "Playing: Groove Salad", "pause while stopped resumes the last channel")

	out = captureStdout(t, func() { runPause() })
	assert.Contains(t, out, "Paused")
}

func TestRunPlayRelative_PassesDelta(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runPlayRelative(-1) })

	assert.Contains(t, out, "Playing:")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, []int{-1}, d.deltas)
}

func TestRunStatus_HumanReadable(t *testing.T) {
	d := startFakeDaemon(t)
	d.mu.Lock()
	d.setPlayingLocked("dronezone")
	d.status.TrackTitle = "Some Track"
	d.mu.Unlock()

	out := captureStdout(t, func() { runStatus(nil) })

	assert.Contains(t, out, "Playing: Drone Zone")
	assert.Contains(t, out, "Track:   Some Track")
	assert.Contains(t, out, "Volume:  50%")
}

func TestRunVolume_ShowSetAndAdjust(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runVolume(nil) })
	assert.Contains(t, out, "Volume:  50%")

	out = captureStdout(t, func() { runVolume([]string{"80"}) })
	assert.Contains(t, out, "Volume:  80%")

	// Relative adjustments apply to the server's current volume.
	out = captureStdout(t, func() { runVolume([]string{"-30"}) })
	assert.Contains(t, out, "Volume:  50%")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.InDelta(t, 0.5, d.status.Volume, 1e-9)
}

func TestRunFavorite_ToggleWithJSON(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runFavorite([]string{"--json", "groove"}) })

	var res favoriteResult
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.Equal(t, favoriteResult{ChannelID: "groovesalad", Title: "Groove Salad", Favorite: true}, res)
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.ElementsMatch(t, []string{"dronezone", "groovesalad"}, d.payload.Favorites)
}

func TestRunServerStop_RequestsShutdown(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runServerStop() })

	assert.Contains(t, out, "server stopped")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.shutdowns)
}

func TestRunServerStop_NotRunning(t *testing.T) {
	setEndpoint(t, client.UnixEndpoint(filepath.Join(t.TempDir(), "absent.sock")))

	out := captureStdout(t, func() { runServerStop() })

	assert.Contains(t, out, "server not running")
}

func TestUserAgent_CarriesVersion(t *testing.T) {
	assert.Contains(t, userAgent(), version)
}
