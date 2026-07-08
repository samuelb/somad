//go:build darwin || linux

// Package tray renders a system-tray / menu-bar item for the running server
// with basic playback controls. It mirrors playback state pushed by the server
// and routes menu clicks back through the same command sender MPRIS uses.
package tray

import (
	_ "embed"
	"runtime"
	"sync"

	"fyne.io/systray"

	"somad/internal/platform"
)

// Supported reports whether this build has a real tray implementation.
const Supported = true

//go:embed icon.png
var iconPNG []byte

//go:embed icon_template.png
var iconTemplatePNG []byte

// Channel is a catalog entry the tray offers for playback. Favorites-first
// ordering is the caller's responsibility; Favorite only drives the ★ marker
// and the main menu's Favorite checkbox while the channel plays.
type Channel struct {
	ID       string
	Title    string
	Favorite bool
}

// Tray owns the menu-bar item. Its methods are safe to call from any
// goroutine, but Run must be called on the main goroutine because systray owns
// the process's native GUI run loop.
type Tray struct {
	mu     sync.Mutex
	sender platform.CmdSender
	onQuit func()

	ready     bool
	playing   bool
	playingID string
	station   string
	track     string
	channels  []Channel

	titleItem   *systray.MenuItem
	playStop    *systray.MenuItem
	favItem     *systray.MenuItem
	channelsTop *systray.MenuItem
	// channelItems is a grow-only pool of submenu items reused across catalog
	// updates: systray cannot cleanly remove items, so surplus slots are
	// hidden rather than removed. channelSlotID[i] is the channel each visible
	// slot currently plays, read by its click watcher.
	channelItems  []*systray.MenuItem
	channelSlotID []string
}

// New creates a Tray. It does not start the run loop; call Run for that.
func New() *Tray { return &Tray{} }

// SetSender wires the command sink that menu clicks are routed to. It reuses
// the same interface and message types as MPRIS, so the server's existing
// command router handles tray clicks unchanged.
func (t *Tray) SetSender(s platform.CmdSender) {
	t.mu.Lock()
	t.sender = s
	t.mu.Unlock()
}

// SetOnQuit sets the callback invoked when the user picks "Quit" in the menu.
func (t *Tray) SetOnQuit(fn func()) {
	t.mu.Lock()
	t.onQuit = fn
	t.mu.Unlock()
}

// Run builds the menu and blocks on the native GUI loop until Quit is called.
// onReady, if non-nil, runs once the menu is live. Must be called on the main
// goroutine.
func (t *Tray) Run(onReady func()) {
	systray.Run(func() {
		t.build()
		if onReady != nil {
			onReady()
		}
	}, func() {})
}

// Quit tears the tray down and unblocks Run. Safe to call before Run has
// reached readiness.
func (t *Tray) Quit() { systray.Quit() }

// SetPlaying mirrors an active stream into the menu. channelID marks the
// matching entry in the channel list.
func (t *Tray) SetPlaying(channelID, station, track string) {
	t.mu.Lock()
	t.playing = true
	t.playingID = channelID
	t.station = platform.SanitizeUTF8(station)
	t.track = platform.SanitizeUTF8(track)
	t.applyLocked()
	t.renderChannelsLocked()
	t.mu.Unlock()
}

// SetStopped mirrors stopped playback into the menu.
func (t *Tray) SetStopped() {
	t.mu.Lock()
	t.playing = false
	t.playingID = ""
	t.station = ""
	t.track = ""
	t.applyLocked()
	t.renderChannelsLocked()
	t.mu.Unlock()
}

// SetChannels updates the channel picker. The list is copied so the caller may
// reuse its slice. Safe to call before the menu is built; it is applied on
// readiness.
func (t *Tray) SetChannels(channels []Channel) {
	t.mu.Lock()
	t.channels = append(t.channels[:0:0], channels...)
	for i := range t.channels {
		t.channels[i].Title = platform.SanitizeUTF8(t.channels[i].Title)
	}
	// A catalog push may carry a changed favorite flag for the playing
	// channel; applyLocked keeps the main menu's Favorite checkbox in sync.
	t.applyLocked()
	t.renderChannelsLocked()
	t.mu.Unlock()
}

// build populates the menu and starts the click-dispatch goroutine. Runs on
// the GUI thread via Run's onReady.
func (t *Tray) build() {
	if runtime.GOOS == "darwin" {
		// A template icon adapts to the light/dark menu bar automatically.
		systray.SetTemplateIcon(iconTemplatePNG, iconTemplatePNG)
	} else {
		systray.SetIcon(iconPNG)
	}
	systray.SetTitle("")
	systray.SetTooltip("Soma")

	title := systray.AddMenuItem("Stopped", "")
	title.Disable()
	systray.AddSeparator()
	playStop := systray.AddMenuItem("Play", "Play or stop playback")
	next := systray.AddMenuItem("Next", "Next channel")
	prev := systray.AddMenuItem("Previous", "Previous channel")
	fav := systray.AddMenuItemCheckbox("★ Favorite", "Mark or unmark the playing channel as favorite", false)
	systray.AddSeparator()
	channelsTop := systray.AddMenuItem("Channels", "Play a channel")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit soma daemon", "Shut down the playback server")

	t.mu.Lock()
	t.titleItem = title
	t.playStop = playStop
	t.favItem = fav
	t.channelsTop = channelsTop
	t.ready = true
	t.applyLocked()
	t.renderChannelsLocked()
	t.mu.Unlock()

	go func() {
		for {
			select {
			case <-playStop.ClickedCh:
				t.send(platform.MPRISPlayPauseMsg{})
			case <-next.ClickedCh:
				t.send(platform.MPRISNextMsg{})
			case <-prev.ClickedCh:
				t.send(platform.MPRISPrevMsg{})
			case <-fav.ClickedCh:
				t.mu.Lock()
				id := t.playingID
				t.mu.Unlock()
				if id != "" {
					t.send(platform.ToggleFavoriteMsg{ID: id})
				}
			case <-quit.ClickedCh:
				t.quit()
			}
		}
	}()
}

// applyLocked pushes the current playback state onto the menu labels. The
// caller holds t.mu; it is a no-op until the menu exists.
func (t *Tray) applyLocked() {
	if !t.ready {
		return
	}
	if t.playing {
		label := playbackLabel(t.station, t.track)
		t.titleItem.SetTitle("♪ " + label)
		t.playStop.SetTitle("Stop")
		systray.SetTooltip("Soma — " + label)
		t.favItem.Enable()
		if t.favoriteLocked(t.playingID) {
			t.favItem.Check()
		} else {
			t.favItem.Uncheck()
		}
	} else {
		t.titleItem.SetTitle("Stopped")
		t.playStop.SetTitle("Play")
		systray.SetTooltip("Soma")
		t.favItem.Disable()
		t.favItem.Uncheck()
	}
}

// favoriteLocked reports whether the given channel is a favorite in the last
// catalog push. The caller holds t.mu.
func (t *Tray) favoriteLocked(id string) bool {
	for _, ch := range t.channels {
		if ch.ID == id {
			return ch.Favorite
		}
	}
	return false
}

// renderChannelsLocked syncs the channel submenu with t.channels. The item
// pool only grows: extra channels get new items (with a click watcher each),
// and slots beyond the current list are hidden. The caller holds t.mu; it is a
// no-op until the menu exists.
func (t *Tray) renderChannelsLocked() {
	if !t.ready || t.channelsTop == nil {
		return
	}
	for len(t.channelItems) < len(t.channels) {
		idx := len(t.channelItems)
		item := t.channelsTop.AddSubMenuItem("", "")
		t.channelItems = append(t.channelItems, item)
		t.channelSlotID = append(t.channelSlotID, "")
		go t.watchChannelClick(idx, item)
	}
	for i, item := range t.channelItems {
		if i < len(t.channels) {
			ch := t.channels[i]
			t.channelSlotID[i] = ch.ID
			item.SetTitle(channelItemTitle(ch, ch.ID == t.playingID))
			item.Show()
		} else {
			t.channelSlotID[i] = ""
			item.Hide()
		}
	}
}

// watchChannelClick plays whatever channel slot idx currently maps to. Slots
// are reused across catalog updates, so the ID is read at click time.
func (t *Tray) watchChannelClick(idx int, item *systray.MenuItem) {
	for range item.ClickedCh {
		if id := t.slotID(idx); id != "" {
			t.send(platform.PlayChannelMsg{ID: id})
		}
	}
}

// slotID returns the channel ID slot idx currently maps to, or "" for a
// hidden or not-yet-assigned slot.
func (t *Tray) slotID(idx int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if idx < len(t.channelSlotID) {
		return t.channelSlotID[idx]
	}
	return ""
}

// channelItemTitle marks the playing channel with a leading ▸ and favorites
// with a ★.
func channelItemTitle(ch Channel, playing bool) string {
	title := ch.Title
	if ch.Favorite {
		title = "★ " + title
	}
	if playing {
		title = "▸ " + title
	}
	return title
}

// playbackLabel renders the "station — track" now-playing string, tolerating
// either part being empty.
func playbackLabel(station, track string) string {
	switch {
	case station != "" && track != "":
		return station + " — " + track
	case station != "":
		return station
	case track != "":
		return track
	default:
		return "Playing"
	}
}

func (t *Tray) send(msg any) {
	t.mu.Lock()
	s := t.sender
	t.mu.Unlock()
	if s != nil {
		s.Send(msg)
	}
}

func (t *Tray) quit() {
	t.mu.Lock()
	fn := t.onQuit
	t.mu.Unlock()
	if fn != nil {
		fn()
	} else {
		systray.Quit()
	}
}
