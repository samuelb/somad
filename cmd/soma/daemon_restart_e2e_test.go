package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/client"
	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests here run the real daemon through the loss of its audio device
// (ADR-0037): asked to play after it, the daemon starts again as a new
// process, the only thing that gets a working device, and plays there.

// lostDeviceEnv, when set along with mainArgsEnv, gives the daemon run from
// the test binary a lostOncePlayer, with this path as its marker.
const lostDeviceEnv = "SOMA_TEST_LOST_DEVICE"

// lostOncePlayer is an audio player without a device. Until its marker
// file exists, a play fails the way the real player's does once the device
// is gone for good, and leaves the marker; from then on, which is to say
// in the process started after that one, every play succeeds, in silence.
type lostOncePlayer struct{ marker string }

func (p *lostOncePlayer) Play(string, string, uint64) error {
	if _, err := os.Stat(p.marker); err != nil {
		if err := os.WriteFile(p.marker, nil, 0o600); err != nil {
			return err
		}
		return fmt.Errorf("audio device failed: unplugged by the test: %w", audio.ErrAudioDeviceLost)
	}
	return nil
}

func (*lostOncePlayer) Stop(uint64)                          {}
func (*lostOncePlayer) Errors() <-chan error                 { return nil }
func (*lostOncePlayer) TrackUpdates() <-chan audio.TrackInfo { return nil }
func (*lostOncePlayer) SetVolume(float64)                    {}
func (*lostOncePlayer) Volume() float64                      { return 1 }
func (*lostOncePlayer) Spectrum(*audio.Spectrum) bool        { return false }

// lostDeviceSandbox prepares dir for a daemon whose audio device is lost
// at its first play: a cached catalog of one channel, groovesalad, whose
// playlist a server here serves, and the environment that gives the daemon
// its lostOncePlayer.
func lostDeviceSandbox(t *testing.T, dir string) (env []string) {
	t.Helper()
	var playlist string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(playlist))
	}))
	playlist = "[playlist]\nNumberOfEntries=1\nFile1=http://" + srv.Listener.Addr().String() + "/stream\n"
	srv.Start()
	t.Cleanup(srv.Close)
	catalog, err := json.Marshal(channels.Channels{Channels: []channels.Channel{{
		ID:        "groovesalad",
		Title:     "Groove Salad",
		Playlists: []channels.Playlist{{URL: srv.URL + "/groovesalad.pls", Format: audio.FormatMP3, Quality: "highest"}},
	}}})
	require.NoError(t, err)
	// Where a daemon with XDG_CACHE_HOME=dir looks for it.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "somad"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "somad", "somafm_channels.json"), catalog, 0o600))
	return []string{lostDeviceEnv + "=" + filepath.Join(dir, "device-lost")}
}

// loseDevice asks the daemon to play, which is how it finds its device
// gone, and returns the client it asked through.
func loseDevice(t *testing.T, d *daemonProcess) *client.Client {
	t.Helper()
	c := d.connect(t)
	_, err := c.Play("groovesalad")
	require.ErrorContains(t, err, "audio device failed", "%s", d.out)
	st, err := c.Status()
	require.NoError(t, err)
	require.True(t, st.DeviceLost)
	return c
}

// playing reports whether a daemon answers on ep that plays groovesalad,
// and its hello.
func playing(ep client.Endpoint) (protocol.HelloResult, bool) {
	c, err := client.DialEndpoint(ep)
	if err != nil {
		return protocol.HelloResult{}, false
	}
	defer func() { _ = c.Close() }()
	hr, err := c.Hello(version)
	if err != nil {
		return hr, false
	}
	st, err := c.Status()
	return hr, err == nil && st.Status == protocol.StatusPlaying && st.ChannelID == "groovesalad"
}

func TestDaemon_AskedToPlayWithoutItsAudioDeviceStartsAgainInPlaceAndPlays(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	d := startDaemonProcessEnv(t, dir, lostDeviceSandbox(t, dir))
	c := loseDevice(t, d)
	pid := c.ServerInfo().PID

	// As the tray, MPRIS or a remote client would: none of them can
	// replace the daemon the way a local client does before it plays.
	_, err := c.Play("groovesalad")
	require.ErrorContains(t, err, "restarting to play Groove Salad", "%s", d.out)

	var hr protocol.HelloResult
	require.Eventually(t, func() bool {
		var ok bool
		hr, ok = playing(d.ep)
		return ok
	}, 15*time.Second, 20*time.Millisecond, "the daemon must come back playing:\n%s", d.out)
	assert.Equal(t, pid, hr.PID, "the same process to whatever started it")
	assert.False(t, hr.DeviceLost)
	_, statErr := os.Stat(filepath.Join(dir, "somad", "resume.json"))
	assert.True(t, os.IsNotExist(statErr), "the note is for that one start")

	nc := d.connect(t)
	require.NoError(t, ignoreDisconnect(nc.Shutdown()))
	assert.Zero(t, d.exitStatus(t), "%s", d.out)
}

func TestDaemon_SupervisedAndAskedToPlayWithoutItsAudioDeviceExitsToBeStartedAgain(t *testing.T) {
	t.Parallel()
	dir := shortTempDir(t)
	env := lostDeviceSandbox(t, dir)
	d := startDaemonProcessEnv(t, dir, env, "--supervised")
	c := loseDevice(t, d)

	_, err := c.Play("groovesalad")
	require.ErrorContains(t, err, "restarting to play Groove Salad", "%s", d.out)

	// The status its service manager starts it again for (ADR-0038).
	assert.Equal(t, restartExitStatus, d.exitStatus(t), "%s", d.out)

	// Which this test does in its place.
	again := startDaemonProcessEnv(t, dir, env, "--supervised")
	require.Eventually(t, func() bool {
		_, ok := playing(again.ep)
		return ok
	}, 15*time.Second, 20*time.Millisecond, "the daemon started in its place must play:\n%s", again.out)

	nc := again.connect(t)
	require.NoError(t, ignoreDisconnect(nc.Shutdown()))
	assert.Zero(t, again.exitStatus(t), "%s", again.out)
}
