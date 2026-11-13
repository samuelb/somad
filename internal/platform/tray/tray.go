//go:build darwin || linux

// Package tray renders a system-tray / menu-bar item for the running server
// with basic playback controls. It mirrors playback state pushed by the server
// and routes menu clicks back through the same command sender MPRIS uses.
package tray

import (
	_ "embed"
	"log"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"somad/internal/platform"
)

// Supported reports whether this build has a real tray implementation.
const Supported = true

// The icons are rendered from the SVGs beside them, the website favicon's
// broadcast mark: the icon_template ones at 32 px, the 16 pt systray draws
// on macOS at 2x, the others at 64 px for HiDPI Linux panels:
//
//	rsvg-convert -w 32 -h 32 icon_template.svg -o icon_template.png
//	rsvg-convert -w 64 -h 64 icon.svg -o icon.png
//
// and the same for the _stopped and _tuning variants. The full mark is
// playing; _stopped dims both pairs of arcs, _tuning the outer pair only.
// On macOS they are template images, which the menu bar recolours for its
// light or dark appearance, so the states differ by opacity, not colour.
var (
	//go:embed icon.png
	iconPNG []byte
	//go:embed icon_stopped.png
	iconStoppedPNG []byte
	//go:embed icon_tuning.png
	iconTuningPNG []byte
	//go:embed icon_template.png
	iconTemplatePNG []byte
	//go:embed icon_template_stopped.png
	iconTemplateStoppedPNG []byte
	//go:embed icon_template_tuning.png
	iconTemplateTuningPNG []byte
)

// icon is one of the tray's icons.
type icon int

const (
	iconStopped icon = iota // the arcs dimmed
	iconTuning              // the inner arcs lit
	iconPlaying             // the full mark
)

func (i icon) String() string {
	return [...]string{"stopped", "tuning", "playing"}[i]
}

// tuningFrames is the animation the icon plays while a stream connects or
// reconnects: the arcs light up from the dot outwards. It starts at
// tuningStartFrame, so the icon changes at once from either still icon.
var tuningFrames = [...]icon{iconStopped, iconTuning, iconPlaying}

const tuningStartFrame = 1

// tuningFrameInterval is how long each frame of tuningFrames shows: about a
// second for the cycle. On Linux each frame is a D-Bus property change and
// a signal to the panel, so it stays slow, and runs only while tuning.
const tuningFrameInterval = 350 * time.Millisecond

// setTrayIcon shows i as the tray's icon.
func setTrayIcon(i icon) {
	colour := [...][]byte{iconStoppedPNG, iconTuningPNG, iconPNG}[i]
	if runtime.GOOS == "darwin" {
		// A template icon adapts to the light/dark menu bar automatically.
		template := [...][]byte{iconTemplateStoppedPNG, iconTemplateTuningPNG, iconTemplatePNG}[i]
		systray.SetTemplateIcon(template, colour)
	} else {
		systray.SetIcon(colour)
	}
}

// mode is the playback state the tray shows.
type mode int

const (
	modeStopped mode = iota
	modeConnecting
	modeReconnecting
	modePlaying
)

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
	quitting  bool
	mode      mode
	playingID string // the channel playing or tuning in
	station   string
	track     string
	channels  []Channel

	// The menu, from attach on: the items the playback state shows in, and
	// how to add an item to the channel picker and set the icon's tooltip.
	// tooltip is the one last set.
	titleItem  *item
	playStop   *item
	favItem    *item
	addChannel func(slot int) menuItem
	setTooltip func(string)
	tooltip    string
	// setIcon shows an icon, which is icon now. While the mode is connecting
	// or reconnecting, animStop is open and a goroutine steps frame through
	// tuningFrames every frameInterval; leaving the mode closes it.
	setIcon       func(icon)
	icon          icon
	frame         int
	animStop      chan struct{}
	frameInterval time.Duration
	// channelItems is a grow-only pool of submenu items reused across catalog
	// updates: systray cannot cleanly remove items, so surplus slots are
	// hidden rather than removed. channelSlotID[i] is the channel each visible
	// slot currently plays, read by its click watcher.
	channelItems  []*item
	channelSlotID []string
}

// menuItem is what the tray does to a menu item, as systray.MenuItem does
// it, so a test can stand in for the menu.
type menuItem interface {
	SetTitle(title string)
	Show()
	Hide()
	Enable()
	Disable()
	Check()
	Uncheck()
}

// item is a menu item and what it was last set to; its setters skip a call
// that would change nothing. systray hands every call on to the menu's
// host, changed or not: on Linux each is two D-Bus signals and a property
// change, the last of them an invitation to fetch the whole menu again.
// With some 45 channels, setting every slot's title and showing it on each
// track change came to several hundred messages for one new label, sent
// while the server held its lock.
type item struct {
	menuItem
	title    string
	hidden   bool
	disabled bool
	checked  bool
}

// newItem wraps an item just added to the menu under title: visible,
// enabled, and unchecked.
func newItem(m menuItem, title string) *item {
	return &item{menuItem: m, title: title}
}

// setTitle shows title on the item as it is written: track and channel
// names come from the network, and a menu may read more into a label than
// its text (menuLabel).
func (i *item) setTitle(title string) {
	if title != i.title {
		i.title = title
		i.SetTitle(menuLabel(title))
	}
}

func (i *item) setVisible(visible bool) {
	if visible == i.hidden {
		i.hidden = !visible
		if visible {
			i.Show()
		} else {
			i.Hide()
		}
	}
}

func (i *item) setEnabled(enabled bool) {
	if enabled == i.disabled {
		i.disabled = !enabled
		if enabled {
			i.Enable()
		} else {
			i.Disable()
		}
	}
}

func (i *item) setChecked(checked bool) {
	if checked != i.checked {
		i.checked = checked
		if checked {
			i.Check()
		} else {
			i.Uncheck()
		}
	}
}

// New creates a Tray. It does not start the run loop; call Run for that.
func New() *Tray { return &Tray{frameInterval: tuningFrameInterval} }

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
//
// A panic in the loop ends the tray, not the process: Run logs it and
// returns, and Quit stays safe to call. On Linux systray's start returns
// early when it cannot set itself up on the session bus, leaving its
// connection nil, and its teardown then closes that nil connection at quit,
// in the middle of the server's shutdown.
func (t *Tray) Run(onReady func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tray: the tray ended with a panic, the server carries on without it: %v", r)
		}
	}()
	systray.Run(func() {
		t.build()
		if onReady != nil {
			onReady()
		}
	}, func() {})
}

// Quit tears the tray down and unblocks Run. Safe to call before Run has
// reached readiness.
func (t *Tray) Quit() {
	t.mu.Lock()
	t.quitting = true
	t.stopAnimationLocked()
	t.mu.Unlock()
	systray.Quit()
}

// SetPlaying mirrors an active stream into the menu. channelID marks the
// matching entry in the channel list.
func (t *Tray) SetPlaying(channelID, station, track string) {
	t.update(func() {
		t.mode = modePlaying
		t.playingID = channelID
		t.station = platform.SanitizeUTF8(station)
		t.track = platform.SanitizeUTF8(track)
	})
}

// SetConnecting mirrors a stream being tuned in, or reconnected after it
// dropped, into the menu, and animates the icon until the state changes.
// channelID marks the matching entry in the channel list.
func (t *Tray) SetConnecting(channelID, station string, reconnecting bool) {
	t.update(func() {
		t.mode = modeConnecting
		if reconnecting {
			t.mode = modeReconnecting
		}
		t.playingID = channelID
		t.station = platform.SanitizeUTF8(station)
		t.track = ""
	})
}

// SetStopped mirrors stopped playback into the menu.
func (t *Tray) SetStopped() {
	t.update(func() {
		t.mode = modeStopped
		t.playingID = ""
		t.station = ""
		t.track = ""
	})
}

// update runs mutate under the lock, then re-renders the main menu and the
// channel picker from the new state.
func (t *Tray) update(mutate func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	mutate()
	t.applyLocked()
	t.renderChannelsLocked()
}

// SetChannels updates the channel picker. The list is copied so the caller may
// reuse its slice. Safe to call before the menu is built; it is applied on
// readiness.
func (t *Tray) SetChannels(channels []Channel) {
	// A catalog push may carry a changed favorite flag for the playing
	// channel; update's applyLocked keeps the main menu's Favorite checkbox
	// in sync.
	t.update(func() {
		t.channels = append(t.channels[:0:0], channels...)
		for i := range t.channels {
			t.channels[i].Title = platform.SanitizeUTF8(t.channels[i].Title)
		}
	})
}

// build populates the menu and starts the click-dispatch goroutine. Runs on
// the GUI thread via Run's onReady.
func (t *Tray) build() {
	setTrayIcon(iconStopped)
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

	titleItem := newItem(title, "Stopped")
	titleItem.setEnabled(false)
	t.attach(titleItem, newItem(playStop, "Play"), newItem(fav, "★ Favorite"),
		func(slot int) menuItem {
			added := channelsTop.AddSubMenuItem("", "")
			go t.watchChannelClick(slot, added)
			return added
		}, systray.SetTooltip, "Soma", setTrayIcon, iconStopped)

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

// attach hands the tray its menu, as build made it: the title, play/stop
// and favorite items, how to add an item to the channel picker (slot is
// its index, for its click watcher), how to set the icon's tooltip, which
// is tooltip now, and how to set the icon, which is ic now. It then shows
// the state set so far.
func (t *Tray) attach(title, playStop, fav *item, addChannel func(slot int) menuItem, setTooltip func(string), tooltip string, setIcon func(icon), ic icon) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.titleItem, t.playStop, t.favItem = title, playStop, fav
	t.addChannel, t.setTooltip, t.tooltip = addChannel, setTooltip, tooltip
	t.setIcon, t.icon = setIcon, ic
	t.ready = true
	t.applyLocked()
	t.renderChannelsLocked()
}

// applyLocked pushes the current playback state onto the menu labels, those
// of them that change (see item). The caller holds t.mu; it is a no-op until
// the menu exists.
func (t *Tray) applyLocked() {
	if !t.ready {
		return
	}
	title, playStop, tooltip := "Stopped", "Play", "Soma"
	switch t.mode {
	case modePlaying:
		label := playbackLabel(t.station, t.track)
		title, playStop, tooltip = "♪ "+label, "Stop", "Soma — "+label
	case modeConnecting, modeReconnecting:
		label := tuningLabel(t.station, t.mode == modeReconnecting)
		title, playStop, tooltip = label, "Stop", "Soma — "+label
	}
	t.titleItem.setTitle(title)
	t.playStop.setTitle(playStop)
	if tooltip != t.tooltip {
		t.tooltip = tooltip
		t.setTooltip(tooltip)
	}
	active := t.mode != modeStopped
	t.favItem.setEnabled(active)
	t.favItem.setChecked(active && t.favoriteLocked(t.playingID))

	tuning := t.mode == modeConnecting || t.mode == modeReconnecting
	if tuning && t.animStop == nil && !t.quitting {
		t.frame = tuningStartFrame
		stop := make(chan struct{})
		t.animStop = stop
		go t.animate(stop)
	} else if !tuning {
		t.stopAnimationLocked()
	}
	t.showIconLocked()
}

// showIconLocked sets the icon the mode calls for, and the animation's
// frame while tuning, unless it is the one showing. The caller holds t.mu.
func (t *Tray) showIconLocked() {
	want := iconStopped
	switch {
	case t.animStop != nil:
		want = tuningFrames[t.frame]
	case t.mode == modePlaying:
		want = iconPlaying
	}
	if want != t.icon {
		t.icon = want
		t.setIcon(want)
	}
}

// stopAnimationLocked ends the tuning animation, if it runs. The caller
// holds t.mu.
func (t *Tray) stopAnimationLocked() {
	if t.animStop != nil {
		close(t.animStop)
		t.animStop = nil
	}
}

// animate steps the tuning animation until stop is closed.
func (t *Tray) animate(stop chan struct{}) {
	ticker := time.NewTicker(t.frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			t.nextFrame(stop)
		}
	}
}

// nextFrame shows the tuning animation's next frame, unless the animation
// that stop belongs to has ended meanwhile.
func (t *Tray) nextFrame(stop chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.animStop != stop {
		return
	}
	t.frame = (t.frame + 1) % len(tuningFrames)
	t.showIconLocked()
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
// and slots beyond the current list are hidden. A slot whose title and
// visibility stay as they are is left alone (see item): a track change
// touches none, a channel change the two whose ▸ mark moves. The caller
// holds t.mu; it is a no-op until the menu exists.
func (t *Tray) renderChannelsLocked() {
	if !t.ready {
		return
	}
	for len(t.channelItems) < len(t.channels) {
		slot := len(t.channelItems)
		t.channelItems = append(t.channelItems, newItem(t.addChannel(slot), ""))
		t.channelSlotID = append(t.channelSlotID, "")
	}
	for i, slot := range t.channelItems {
		if i < len(t.channels) {
			ch := t.channels[i]
			t.channelSlotID[i] = ch.ID
			slot.setTitle(channelItemTitle(ch, ch.ID == t.playingID))
			slot.setVisible(true)
		} else {
			t.channelSlotID[i] = ""
			slot.setVisible(false)
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

// tuningLabel renders the menu's title while a stream connects, or
// reconnects after it dropped.
func tuningLabel(station string, reconnecting bool) string {
	switch {
	case reconnecting && station != "":
		return "Reconnecting to " + station + "…"
	case reconnecting:
		return "Reconnecting…"
	case station != "":
		return "Tuning in to " + station + "…"
	default:
		return "Tuning in…"
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
