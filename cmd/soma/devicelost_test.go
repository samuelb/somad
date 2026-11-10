package main

import (
	"testing"

	"somad/internal/protocol"

	"github.com/stretchr/testify/assert"
)

// startDeviceLostDaemon runs a fakeDaemon of the client's own version whose
// audio device has failed for good (ADR-0037): stopped, and saying so in
// its hello and its status.
func startDeviceLostDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	d := startFakeDaemon(t)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status.DeviceLost = true
	d.status.StreamError = "audio device failed: connection closed (the soma daemon has to be restarted to play again)"
	return d
}

// A daemon that has lost its audio device plays nothing until a new process
// takes its place, so a command that asks for playback puts one there
// first, as it does for a daemon of another version.
func TestRunPlay_ReplacesADaemonWhoseAudioDeviceIsLost(t *testing.T) {
	old := startDeviceLostDaemon(t)
	restart := spawnsReplacement(t)

	out := captureStdout(t, func() { runPlay([]string{"groove"}) })

	assert.Contains(t, out, "Playing: Groove Salad")
	fresh := restart.daemon(t)
	old.mu.Lock()
	assert.Equal(t, 1, old.shutdowns)
	assert.Empty(t, old.plays, "nothing is asked of the daemon that cannot play")
	old.mu.Unlock()
	fresh.mu.Lock()
	defer fresh.mu.Unlock()
	assert.Equal(t, []string{"groovesalad"}, fresh.plays)
}

// Unpausing is asking for playback too: the fresh daemon resumes the last
// channel.
func TestRunPause_ReplacesADaemonWhoseAudioDeviceIsLost(t *testing.T) {
	old := startDeviceLostDaemon(t)
	restart := spawnsReplacement(t)

	captureStdout(t, func() { runPause(nil) })

	fresh := restart.daemon(t)
	old.mu.Lock()
	assert.Equal(t, 1, old.shutdowns)
	assert.Equal(t, protocol.StatusStopped, old.status.Status, "nothing is asked of the daemon that cannot play")
	old.mu.Unlock()
	fresh.mu.Lock()
	defer fresh.mu.Unlock()
	assert.Equal(t, protocol.StatusPlaying, fresh.status.Status)
}

// A command that only looks leaves the daemon as it is, so that the reason
// the music stopped is still there to read.
func TestRunStatus_ShowsWhyADaemonWithALostAudioDeviceIsSilent(t *testing.T) {
	old := startDeviceLostDaemon(t)
	restart := spawnsReplacement(t)

	out := captureStdout(t, func() { runStatus(nil) })

	assert.Contains(t, out, "audio device failed")
	assert.Contains(t, out, "has to be restarted")
	assert.Empty(t, restart.spawned)
	old.mu.Lock()
	defer old.mu.Unlock()
	assert.Zero(t, old.shutdowns)
	assert.Equal(t, protocol.StatusStopped, old.status.Status)
}
