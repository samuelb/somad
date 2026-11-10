//go:build darwin

package platform

import "C"

import (
	"runtime"
	"sync"
	"sync/atomic"
)

func init() {
	// The run loop MediaPlayer needs is the main thread's (RunMainLoop),
	// and this keeps the main goroutine on it.
	runtime.LockOSThread()
}

// nowPlayingCenter is the system's Now Playing as MPRIS uses it; tests
// stand in for it.
type nowPlayingCenter interface {
	// update shows what plays or is paused, and takes the remote commands.
	update(title, artist, station string, playing bool)
	// clear gives both up.
	clear()
}

// MPRIS on macOS is the system's Now Playing (ADR-0039), which does there
// what MPRIS does on Linux: it shows the station and track in Control
// Center and on the lock screen, and routes the media keys, headset buttons
// and the controls shown there to the daemon. It keeps the name its Linux
// counterpart has, so the server drives both through one type.
//
// The process becomes the Now Playing app with its first play, not before:
// an idle daemon must not take the media keys from whatever the user
// plays with. From then on it stays that, shown as paused while stopped,
// until another app plays, so the play key brings the last channel back.
type MPRIS struct {
	center nowPlayingCenter

	senderMu sync.Mutex
	sender   CmdSender

	mu      sync.Mutex
	station string // the last station played; "" before the first play
	closed  bool
}

// current is the MPRIS the remote commands go to: MPRemoteCommandCenter is
// one per process, and so is this.
var current atomic.Pointer[MPRIS]

// NewMPRIS returns the Now Playing integration, or nil without a GUI
// session. Its commands arrive only while the main thread runs a run loop:
// the tray's, or RunMainLoop.
func NewMPRIS() (*MPRIS, error) {
	if !HasGUISession() {
		return nil, nil
	}
	m := &MPRIS{center: systemNowPlaying{}}
	current.Store(m)
	return m, nil
}

// SetSender sets the command sender for the remote commands.
func (m *MPRIS) SetSender(sender CmdSender) {
	m.senderMu.Lock()
	defer m.senderMu.Unlock()
	m.sender = sender
}

// SetPlaying shows the track as playing. The artwork URL is not used: Now
// Playing takes an image, not a URL.
func (m *MPRIS) SetPlaying(station, track, artist, _ string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.station = station
	title := track
	if title == "" {
		title = station
	}
	if artist == station {
		// The server names the station where a title has no artist; it is
		// the album line here already.
		artist = ""
	}
	m.center.update(SanitizeUTF8(title), SanitizeUTF8(artist), SanitizeUTF8(station), true)
}

// SetStopped shows the last station as paused. A stop is how live radio
// pauses, and an app that reports itself stopped is no longer the one the
// play key goes to. Before the first play it does nothing.
func (m *MPRIS) SetStopped() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.station == "" {
		return
	}
	station := SanitizeUTF8(m.station)
	m.center.update(station, "", station, false)
}

// SetVolume does nothing: Now Playing has no volume of its own.
func (m *MPRIS) SetVolume(float64) {}

// Close gives Now Playing and the remote commands up.
func (m *MPRIS) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	current.CompareAndSwap(m, nil)
	m.center.clear()
}

// NeedsMainLoop reports whether the remote commands depend on the caller
// running the main thread's run loop (RunMainLoop). They do, unless
// something else runs it: the tray.
func (m *MPRIS) NeedsMainLoop() bool { return m != nil }

// RunMainLoop runs the main thread's run loop until StopMainLoop. It must
// be called on the main goroutine.
func (m *MPRIS) RunMainLoop() { runMainLoop() }

// StopMainLoop makes RunMainLoop return; it may come first.
func (m *MPRIS) StopMainLoop() { stopMainLoop() }

// somaNowPlayingCommand is called by nowplaying_darwin.go's Objective-C, on
// the main thread, for every remote command.
//
//export somaNowPlayingCommand
func somaNowPlayingCommand(command C.int) { routeCommand(int(command)) }

// routeCommand hands a remote command to the process's MPRIS, if it has one.
func routeCommand(command int) {
	if m := current.Load(); m != nil {
		m.command(command)
	}
}

// command routes a remote command to the sender. It runs on the main
// thread, which the tray draws on, so the sender is called off it: a stop
// updates the tray while it holds the server's lock.
func (m *MPRIS) command(command int) {
	var msg any
	switch command {
	case commandPlay:
		msg = MPRISPlayMsg{}
	case commandTogglePlayPause:
		msg = MPRISPlayPauseMsg{}
	case commandPause, commandStop:
		// Live radio cannot hold its place: pausing stops the stream, as
		// the MPRIS Pause method does on Linux.
		msg = MPRISStopMsg{}
	case commandNext:
		msg = MPRISNextMsg{}
	case commandPrevious:
		msg = MPRISPrevMsg{}
	default:
		return
	}
	m.senderMu.Lock()
	sender := m.sender
	m.senderMu.Unlock()
	if sender != nil {
		go sender.Send(msg)
	}
}
