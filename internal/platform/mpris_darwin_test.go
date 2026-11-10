//go:build darwin

package platform

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nowPlayingUpdate is one call of nowPlayingCenter.update.
type nowPlayingUpdate struct {
	title, artist, station string
	playing                bool
}

// fakeNowPlaying records what MPRIS shows instead of showing it: the real
// Now Playing would put the test run into the media controls of whoever
// runs it.
type fakeNowPlaying struct {
	mu      sync.Mutex
	updates []nowPlayingUpdate
	cleared int
}

func (f *fakeNowPlaying) update(title, artist, station string, playing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, nowPlayingUpdate{title, artist, station, playing})
}

func (f *fakeNowPlaying) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared++
}

func (f *fakeNowPlaying) last(t *testing.T) nowPlayingUpdate {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.updates)
	return f.updates[len(f.updates)-1]
}

func (f *fakeNowPlaying) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updates)
}

// chanSender hands the messages it is sent to the test.
type chanSender chan any

func (c chanSender) Send(msg any) { c <- msg }

func newTestNowPlaying(t *testing.T) (*MPRIS, *fakeNowPlaying) {
	t.Helper()
	center := &fakeNowPlaying{}
	m := &MPRIS{center: center}
	current.Store(m)
	t.Cleanup(func() { current.CompareAndSwap(m, nil) })
	return m, center
}

func TestNowPlaying_ShowsTheTrackUnderItsStation(t *testing.T) {
	m, center := newTestNowPlaying(t)

	m.SetPlaying("Groove Salad", "City Lights", "Christophe Goze", "https://somafm.com/img/groovesalad.png")

	assert.Equal(t, nowPlayingUpdate{"City Lights", "Christophe Goze", "Groove Salad", true}, center.last(t))
}

func TestNowPlaying_ShowsTheStationWhereThereIsNoTrackOrArtist(t *testing.T) {
	m, center := newTestNowPlaying(t)

	// Before the stream's first title.
	m.SetPlaying("Drone Zone", "", "Drone Zone", "")
	assert.Equal(t, nowPlayingUpdate{"Drone Zone", "", "Drone Zone", true}, center.last(t))

	// The server names the station as the artist of a title without one:
	// it is not shown twice.
	m.SetPlaying("Drone Zone", "Station ID", "Drone Zone", "")
	assert.Equal(t, nowPlayingUpdate{"Station ID", "", "Drone Zone", true}, center.last(t))
}

func TestNowPlaying_AnIdleDaemonStaysOutOfIt(t *testing.T) {
	m, center := newTestNowPlaying(t)

	// What the server reports at start, and whenever nothing plays.
	m.SetStopped()
	m.SetVolume(0.5)

	// Nothing yet makes the daemon the app the media keys go to.
	assert.Zero(t, center.count())
}

func TestNowPlaying_AStopShowsTheLastStationPaused(t *testing.T) {
	m, center := newTestNowPlaying(t)
	m.SetPlaying("Groove Salad", "City Lights", "Christophe Goze", "")

	m.SetStopped()

	// Paused, not stopped: the play key still comes here.
	assert.Equal(t, nowPlayingUpdate{"Groove Salad", "", "Groove Salad", false}, center.last(t))
}

func TestNowPlaying_RoutesTheRemoteCommands(t *testing.T) {
	m, _ := newTestNowPlaying(t)
	sender := make(chanSender, 1)
	m.SetSender(sender)

	for command, want := range map[int]any{
		commandPlay:            MPRISPlayMsg{},
		commandTogglePlayPause: MPRISPlayPauseMsg{},
		commandNext:            MPRISNextMsg{},
		commandPrevious:        MPRISPrevMsg{},
		// Live radio pauses by stopping.
		commandPause: MPRISStopMsg{},
		commandStop:  MPRISStopMsg{},
	} {
		routeCommand(command)
		select {
		case got := <-sender:
			assert.Equal(t, want, got)
		case <-time.After(5 * time.Second):
			t.Fatalf("command %d sent nothing", command)
		}
	}
}

func TestNowPlaying_DropsWhatItCannotRoute(t *testing.T) {
	m, _ := newTestNowPlaying(t)

	// No sender yet, and a command this build does not know.
	m.command(commandPlay)
	sender := make(chanSender, 1)
	m.SetSender(sender)
	m.command(99)

	select {
	case msg := <-sender:
		t.Fatalf("sent %#v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNowPlaying_CloseGivesItUp(t *testing.T) {
	m, center := newTestNowPlaying(t)
	sender := make(chanSender, 1)
	m.SetSender(sender)
	m.SetPlaying("Groove Salad", "City Lights", "Christophe Goze", "")

	m.Close()
	m.Close()

	assert.Equal(t, 1, center.cleared)
	// The server may still report a state on its way down, and the system
	// a command.
	m.SetPlaying("Groove Salad", "Flying Home", "Charlie North", "")
	m.SetStopped()
	assert.Equal(t, 1, center.count())
	routeCommand(commandPlay)
	select {
	case msg := <-sender:
		t.Fatalf("sent %#v", msg)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNowPlaying_OnlyOneThatExistsNeedsTheMainLoop(t *testing.T) {
	var none *MPRIS
	assert.False(t, none.NeedsMainLoop())
	m, _ := newTestNowPlaying(t)
	assert.True(t, m.NeedsMainLoop())
}

func TestHasGUISessionDoesNotPanic(t *testing.T) {
	// Its result depends on where the tests run.
	_ = HasGUISession()
}
