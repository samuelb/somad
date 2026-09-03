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
	"time"

	"somad/internal/channels"
	"somad/internal/client"
	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDaemon is a scripted playback daemon for exercising the CLI commands
// end-to-end over the wire protocol, without audio or spawning.
type fakeDaemon struct {
	mu            sync.Mutex
	plays         []string
	deltas        []int
	stops         int
	stopIns       []string // "in" durations passed to successive MethodStop calls
	stopCancels   int
	shutdowns     int
	mutes         int
	preMute       float64 // remembered pre-mute volume; 0 means "none stored"
	status        protocol.PlaybackState
	payload       protocol.ChannelsPayload
	history       []protocol.HistoryEntry
	historyParams []protocol.HistoryParams // one entry per history request received
	lastfmReloads int
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
		var p protocol.StopParams
		_ = json.Unmarshal(req.Params, &p)
		switch {
		case p.Cancel:
			d.stopCancels++
			d.status.StopAt = ""
		case p.In != "":
			d.stopIns = append(d.stopIns, p.In)
			d.status.StopAt = "2099-01-01T00:00:00Z"
		default:
			d.stops++
			d.status = protocol.PlaybackState{Status: protocol.StatusStopped, Volume: d.status.Volume}
		}
		return d.status
	case protocol.MethodSetVolume:
		var p protocol.SetVolumeParams
		_ = json.Unmarshal(req.Params, &p)
		d.status.Volume = p.Volume
		if p.Volume > 0 {
			d.preMute = 0
		}
		return d.status
	case protocol.MethodToggleMute:
		d.mutes++
		if d.status.Volume > 0 {
			d.preMute = d.status.Volume
			d.status.Volume = 0
		} else {
			if d.preMute > 0 {
				d.status.Volume = d.preMute
			} else {
				d.status.Volume = 1
			}
			d.preMute = 0
		}
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
	case protocol.MethodHistory:
		var p protocol.HistoryParams
		_ = json.Unmarshal(req.Params, &p)
		d.historyParams = append(d.historyParams, p)
		return protocol.HistoryResult{Entries: d.history}
	case protocol.MethodReloadLastfm:
		d.lastfmReloads++
		return struct{}{}
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

func TestRunPlay_JSON(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runPlay([]string{"--json", "groove"}) })

	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, protocol.StatusPlaying, st.Status)
	assert.Equal(t, "groovesalad", st.ChannelID)
	assert.Equal(t, "Groove Salad", st.ChannelTitle)
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

	out := captureStdout(t, func() { runStop(nil) })

	assert.Contains(t, out, "Stopped")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.stops)
}

func TestRunStop_JSON(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runStop([]string{"--json"}) })

	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, protocol.StatusStopped, st.Status)
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.stops)
}

func TestRunStop_InArmsTimerWithoutStoppingNow(t *testing.T) {
	d := startFakeDaemon(t)
	d.mu.Lock()
	d.setPlayingLocked("groovesalad")
	d.mu.Unlock()

	out := captureStdout(t, func() { runStop([]string{"--in", "45m"}) })

	// The message echoes what the user typed; what reaches the daemon is the
	// client-parsed duration's canonical Go string ("45m0s"), an equivalent
	// value time.ParseDuration accepts just as well.
	assert.Contains(t, out, "Stopping in 45m")
	d.mu.Lock()
	defer d.mu.Unlock()
	require.Len(t, d.stopIns, 1)
	parsed, err := time.ParseDuration(d.stopIns[0])
	require.NoError(t, err)
	assert.Equal(t, 45*time.Minute, parsed)
	assert.Zero(t, d.stops, "arming a sleep timer must not call the immediate-stop path")
}

func TestRunStop_InJSON(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runStop([]string{"--json", "--in", "10s"}) })

	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.NotEmpty(t, st.StopAt)
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, []string{"10s"}, d.stopIns)
}

func TestRunStop_Cancel(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runStop([]string{"--cancel"}) })

	assert.Contains(t, out, "canceled")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 1, d.stopCancels)
	assert.Zero(t, d.stops)
}

func TestRunPause_TogglesBothWays(t *testing.T) {
	startFakeDaemon(t)

	out := captureStdout(t, func() { runPause(nil) })
	assert.Contains(t, out, "Playing: Groove Salad", "pause while stopped resumes the last channel")

	out = captureStdout(t, func() { runPause(nil) })
	assert.Contains(t, out, "Paused")
}

func TestRunPause_JSON(t *testing.T) {
	startFakeDaemon(t)

	out := captureStdout(t, func() { runPause([]string{"--json"}) })
	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, protocol.StatusPlaying, st.Status, "pause while stopped resumes the last channel")

	out = captureStdout(t, func() { runPause([]string{"--json"}) })
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, protocol.StatusStopped, st.Status)
}

func TestRunPlayRelative_PassesDelta(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runPlayRelative(-1, "prev", nil) })

	assert.Contains(t, out, "Playing:")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, []int{-1}, d.deltas)
}

func TestRunPlayRelative_JSON(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runPlayRelative(1, "next", []string{"--json"}) })

	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.Equal(t, protocol.StatusPlaying, st.Status)
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, []int{1}, d.deltas)
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

func TestRunStatus_ShowsSleepTimer(t *testing.T) {
	d := startFakeDaemon(t)
	d.mu.Lock()
	d.setPlayingLocked("dronezone")
	d.status.StopAt = time.Now().Add(45 * time.Minute).Format(time.RFC3339)
	d.mu.Unlock()

	out := captureStdout(t, func() { runStatus(nil) })

	assert.Contains(t, out, "Sleep:   in 45m")
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

func TestRunVolume_JSON(t *testing.T) {
	startFakeDaemon(t)

	out := captureStdout(t, func() { runVolume([]string{"--json"}) })
	var st protocol.PlaybackState
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.InDelta(t, 0.5, st.Volume, 1e-9)

	out = captureStdout(t, func() { runVolume([]string{"--json", "80"}) })
	require.NoError(t, json.Unmarshal([]byte(out), &st))
	assert.InDelta(t, 0.8, st.Volume, 1e-9)
}

func TestRunVolume_Mute(t *testing.T) {
	d := startFakeDaemon(t)

	out := captureStdout(t, func() { runVolume([]string{"mute"}) })
	assert.Contains(t, out, "Muted")
	d.mu.Lock()
	assert.Equal(t, 1, d.mutes)
	assert.Zero(t, d.status.Volume)
	d.mu.Unlock()

	out = captureStdout(t, func() { runVolume([]string{"mute"}) })
	assert.Contains(t, out, "Volume:  50%")
	d.mu.Lock()
	defer d.mu.Unlock()
	assert.Equal(t, 2, d.mutes)
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

func TestRunHistory_PlainAndJSON(t *testing.T) {
	d := startFakeDaemon(t)
	when := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
	d.mu.Lock()
	d.history = []protocol.HistoryEntry{
		{ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Title: "Some Track", Time: when},
	}
	d.mu.Unlock()

	plain := captureStdout(t, func() { runHistory(nil) })
	assert.Contains(t, plain, "Groove Salad")
	assert.Contains(t, plain, "Some Track")

	jsonOut := captureStdout(t, func() { runHistory([]string{"--json"}) })
	var entries []protocol.HistoryEntry
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &entries))
	require.Len(t, entries, 1)
	assert.Equal(t, "Some Track", entries[0].Title)
}

func TestRunHistory_NoEntries(t *testing.T) {
	startFakeDaemon(t)

	out := captureStdout(t, func() { runHistory(nil) })

	assert.Contains(t, out, "No history yet.")
}

func TestRunHistory_ResolvesChannelAndPassesLimit(t *testing.T) {
	d := startFakeDaemon(t)

	captureStdout(t, func() { runHistory([]string{"-n", "5", "groove"}) })

	d.mu.Lock()
	defer d.mu.Unlock()
	require.Len(t, d.historyParams, 1)
	assert.Equal(t, "groovesalad", d.historyParams[0].ChannelID, "the substring must resolve to the channel ID")
	assert.Equal(t, 5, d.historyParams[0].Limit)
}

func TestRunHistory_DefaultLimitWithNoChannel(t *testing.T) {
	d := startFakeDaemon(t)

	captureStdout(t, func() { runHistory(nil) })

	d.mu.Lock()
	defer d.mu.Unlock()
	require.Len(t, d.historyParams, 1)
	assert.Empty(t, d.historyParams[0].ChannelID)
	assert.Equal(t, historyDefaultLimit, d.historyParams[0].Limit)
}
