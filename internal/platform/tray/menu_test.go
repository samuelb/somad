//go:build darwin || linux

package tray

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeMenu stands in for the menu systray builds and records every call
// that reaches it, in order.
type fakeMenu struct{ calls []string }

func (f *fakeMenu) item(name string) menuItem { return fakeItem{menu: f, name: name} }

// take returns the calls recorded since the last take.
func (f *fakeMenu) take() []string {
	calls := f.calls
	f.calls = nil
	return calls
}

// attach attaches the fake menu to tr as build attaches the real one.
func (f *fakeMenu) attach(tr *Tray) {
	tr.attach(newItem(f.item("title"), "Stopped"), newItem(f.item("play/stop"), "Play"), newItem(f.item("favorite"), "★ Favorite"),
		func(slot int) menuItem { return f.item(fmt.Sprintf("channel %d", slot)) },
		func(tooltip string) { f.calls = append(f.calls, "tooltip: "+tooltip) }, "Soma")
}

type fakeItem struct {
	menu *fakeMenu
	name string
}

func (f fakeItem) record(call string)    { f.menu.calls = append(f.menu.calls, f.name+": "+call) }
func (f fakeItem) SetTitle(title string) { f.record("title " + title) }
func (f fakeItem) Show()                 { f.record("show") }
func (f fakeItem) Hide()                 { f.record("hide") }
func (f fakeItem) Enable()               { f.record("enable") }
func (f fakeItem) Disable()              { f.record("disable") }
func (f fakeItem) Check()                { f.record("check") }
func (f fakeItem) Uncheck()              { f.record("uncheck") }

// trayChannels is a catalog of n channels, c0 to c<n-1>.
func trayChannels(n int) []Channel {
	channels := make([]Channel, n)
	for i := range channels {
		channels[i] = Channel{ID: fmt.Sprintf("c%d", i), Title: fmt.Sprintf("Channel %d", i)}
	}
	return channels
}

// playingTray returns a tray with a catalog the size of SomaFM's, playing
// a track on its third channel, and its menu with the calls so far
// forgotten.
func playingTray() (*Tray, *fakeMenu) {
	tr, menu := New(), &fakeMenu{}
	menu.attach(tr)
	tr.SetChannels(trayChannels(45))
	tr.SetPlaying("c2", "Channel 2", "Artist - One")
	menu.take()
	return tr, menu
}

func TestTray_ATrackChangeTouchesNoChannelSlot(t *testing.T) {
	tr, menu := playingTray()

	tr.SetPlaying("c2", "Channel 2", "Artist - Two")

	assert.Equal(t, []string{
		"title: title ♪ Channel 2 — Artist - Two",
		"tooltip: Soma — Channel 2 — Artist - Two",
	}, menu.take())
}

func TestTray_AChannelChangeTouchesTheTwoSlotsItsMarkMoves(t *testing.T) {
	tr, menu := playingTray()

	tr.SetPlaying("c7", "Channel 7", "")

	assert.Equal(t, []string{
		"title: title ♪ Channel 7",
		"tooltip: Soma — Channel 7",
		"channel 2: title Channel 2",
		"channel 7: title ▸ Channel 7",
	}, menu.take())
}

func TestTray_TheSameStateAgainTouchesNothing(t *testing.T) {
	tr, menu := playingTray()

	tr.SetPlaying("c2", "Channel 2", "Artist - One")
	tr.SetChannels(trayChannels(45))

	assert.Empty(t, menu.take())
}

func TestTray_StoppingPutsBackWhatPlayingSet(t *testing.T) {
	tr, menu := playingTray()

	tr.SetStopped()

	assert.Equal(t, []string{
		"title: title Stopped",
		"play/stop: title Play",
		"tooltip: Soma",
		"favorite: disable",
		"channel 2: title Channel 2",
	}, menu.take())

	tr.SetStopped()
	assert.Empty(t, menu.take(), "and only once")
}

func TestTray_ACatalogPushTouchesTheSlotsThatDiffer(t *testing.T) {
	tr, menu := playingTray()

	// The playing channel became a favorite: its slot, and the checkbox.
	channels := trayChannels(45)
	channels[2].Favorite = true
	tr.SetChannels(channels)
	assert.Equal(t, []string{"favorite: check", "channel 2: title ▸ ★ Channel 2"}, menu.take())

	// A shorter catalog hides the slots past its end, once.
	tr.SetChannels(channels[:43])
	assert.Equal(t, []string{"channel 43: hide", "channel 44: hide"}, menu.take())
	tr.SetChannels(channels[:43])
	assert.Empty(t, menu.take())

	// A longer one shows them again, and adds a slot for what is new.
	tr.SetChannels(append(channels[:45:45], Channel{ID: "new", Title: "New"}))
	assert.Equal(t, []string{"channel 43: show", "channel 44: show", "channel 45: title New"}, menu.take())
	assert.Equal(t, "new", tr.slotID(45))
	assert.Empty(t, tr.slotID(46))
}

func TestTray_StateSetBeforeTheMenuExistsShowsWhenItDoes(t *testing.T) {
	tr, menu := New(), &fakeMenu{}
	tr.SetChannels([]Channel{{ID: "a", Title: "A", Favorite: true}, {ID: "b", Title: "B"}})
	tr.SetPlaying("a", "A", "Track")

	menu.attach(tr)

	assert.Equal(t, []string{
		"title: title ♪ A — Track",
		"play/stop: title Stop",
		"tooltip: Soma — A — Track",
		"favorite: check",
		"channel 0: title ▸ ★ A",
		"channel 1: title B",
	}, menu.take())
}

func TestTray_AttachedWithNothingPlayingDisablesTheFavoriteItem(t *testing.T) {
	tr, menu := New(), &fakeMenu{}

	menu.attach(tr)

	assert.Equal(t, []string{"favorite: disable"}, menu.take())
}
