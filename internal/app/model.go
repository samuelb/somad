package app

import (
	"errors"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

// AboutInfo holds version and metadata for the about screen.
type AboutInfo struct {
	Version string
	Commit  string
	Date    string
}

// Model represents the TUI state. Playback lives in the server; the model
// renders from the latest snapshot and sends commands over the Backend.
type Model struct {
	List    list.Model
	Backend Backend

	// Snapshot is the latest authoritative playback state from the server.
	Snapshot protocol.PlaybackState
	// Favorites mirrors the server-persisted favorite channel IDs.
	Favorites []string
	// PlayingID is derived from Snapshot for the list delegate's playing marker.
	PlayingID string
	// ServerLost is true while the server connection is being re-established.
	ServerLost bool
	// ServerVersion is the version the connected server reports. When it differs
	// from About.Version the server is out of date and the next channel change,
	// pause or stop restarts it onto ours (see skewed).
	ServerVersion string
	// pendingPlayID is a channel to play once the server has been restarted for
	// a version upgrade and the reconnect has delivered a fresh backend.
	pendingPlayID string
	// requestedID is a channel the user just asked to play, marked as tuning
	// in the list from the key press on, before the server answers. It is
	// not playback state: the next snapshot (or a failed request) clears it,
	// and from then on the snapshot says what is connecting.
	requestedID string
	// sleepTickStopAt is the sleep-timer deadline (Snapshot.StopAt) the
	// countdown tick chain was started for, and sleepTickGen that chain's
	// number; ticks from older chains are dropped. See syncSleepTick.
	sleepTickStopAt string
	sleepTickGen    int

	Loading bool
	Err     error
	// RequestErr is the most recent failed-request notice, shown in the
	// status bar until the server next answers successfully.
	RequestErr string
	ShowAbout  bool
	About      AboutInfo
	// ShowHistory toggles the now-playing history overlay. HistoryChannelID
	// and HistoryChannelTitle are the channel it was opened for; History
	// holds the last fetch's entries and HistoryErr its error, if any.
	ShowHistory         bool
	HistoryChannelID    string
	HistoryChannelTitle string
	History             []protocol.HistoryEntry
	HistoryErr          error
	Width               int
	Height              int
	// ShutdownOnExit asks the server to stop playback and exit when the TUI
	// closes. OnExit is called before quitting so the reconnect bridge does not
	// auto-spawn a replacement server.
	ShutdownOnExit bool
	OnExit         func()
	// Search state. While SearchQuery is set, m.List holds only the
	// matching channels (see refreshVisibleItems), so the match count is the
	// list length and the current match is the list cursor.
	Searching   bool   // Whether search input is active
	SearchQuery string // Current search query
	// FavoritesOnly restricts the list to favorite channels, applied on top
	// of the search filter above; see refreshVisibleItems.
	FavoritesOnly bool

	// allItems is the full, favorites-sorted catalog. m.List.Items() shows
	// either allItems or a filtered subset of it (search and/or
	// FavoritesOnly); see refreshVisibleItems in search.go.
	allItems []list.Item
	// matches holds, by channel ID, where the search query matched each
	// visible channel, for the delegate to highlight; nil without a query.
	matches map[string]textMatch

	// Visualizer is the style the spectrum of the playing audio is drawn
	// behind the view in, cycled by v, or ui.VisualizerOff; viz holds what
	// it draws from. vizBands is the band count of the server's spectrum
	// subscription, 0 for none, and vizSubscribing is true while a request
	// to change it is in flight; see syncSpectrum. vizNotice names the
	// style in the now-playing card for a moment after a change, until the
	// vizNoticeMsg of chain vizNoticeGen.
	Visualizer     ui.VisualizerMode
	viz            ui.Visualizer
	vizBands       int
	vizSubscribing bool
	vizNotice      bool
	vizNoticeGen   int
	// OnVisualizer, when set, is told every style v picks, so it can be
	// remembered across restarts; it must not block.
	OnVisualizer func(ui.VisualizerMode)

	// frame counts animation ticks; the playing indicator and the spinners
	// are drawn for it. animating is true while a tick chain is running;
	// see syncAnim.
	frame     int
	animating bool
}

// Init requests the initial catalog and playback state from the server,
// and starts the loading spinner.
func (m *Model) Init() tea.Cmd {
	// A visualizer style remembered from the last run subscribes at once.
	return tea.Batch(m.fetchChannels(), m.fetchStatus(), tea.EnterAltScreen, m.syncAnim(), m.syncSpectrum())
}

// NewList returns the channel list component for m, empty and unsized:
// the styled delegate, dot pagination, and a help keymap showing ours. The
// list's own title, status bar and help give way to RenderHeader,
// RenderSearchBar and RenderHelp, its filter to search, and its own quit
// keys (q, esc, ctrl+c) are disabled so every quit goes through quitCmd
// and honors ShutdownOnExit. Set ShutdownOnExit first; the help reflects it.
func (m *Model) NewList() list.Model {
	delegate := ui.NewStyledDelegate(m.rowInfo, &m.frame)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false)        // We render our own header
	l.SetShowStatusBar(false)    // The header carries the channel count
	l.SetShowHelp(false)         // RenderHelp draws it below the now-playing card
	l.SetFilteringEnabled(false) // Disable filtering, we use search instead
	l.DisableQuitKeybindings()
	// The bubbles default binds "h" to previous page; "h" is used for the
	// history overlay instead (see the keymap in update.go), so drop it here
	// rather than silently shadowing it with no help text to match.
	l.KeyMap.PrevPage = key.NewBinding(
		key.WithKeys("left", "pgup", "b", "u"),
		key.WithHelp("←/pgup", "prev page"),
	)
	l.Paginator.ActiveDot = ui.ActiveDotStyle.Render("● ")
	l.Paginator.InactiveDot = ui.InactiveDotStyle.Render("● ")
	l.Paginator.ArabicFormat = "page %d of %d"
	l.Styles.PaginationStyle = ui.SubtleStyle.PaddingLeft(paginationIndent)
	l.Styles.ArabicPagination = ui.SubtleStyle
	// RenderHelp draws the short help itself (ui.ShortHelp); the full help
	// is the list's.
	l.Help.Styles.FullKey = ui.HelpKeyStyle
	l.Help.Styles.FullDesc = ui.HelpDescStyle
	l.Help.Styles.FullSeparator = ui.HelpSepStyle

	fullHelp, shortHelp := NewHelpKeys(m.ShutdownOnExit)
	l.AdditionalFullHelpKeys = func() []key.Binding {
		return fullHelp
	}
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return shortHelp
	}
	return l
}

// rowInfo tells the delegate what the row for item shows beyond the
// channel: the playing and favorite marks, whether it is tuning in, and
// the search match.
func (m *Model) rowInfo(_ int, item ui.Item) ui.RowInfo {
	id := item.Channel.ID
	match := m.matches[id]
	info := ui.RowInfo{
		Playing:      id != "" && id == m.PlayingID,
		Favorite:     m.isFavoriteID(id),
		TitleMatches: match.title,
		DescMatches:  match.desc,
	}
	if id != "" && id == m.tuningID() {
		info.Tuning = "tuning in…"
		if m.Snapshot.Status == protocol.StatusReconnecting && id == m.Snapshot.ChannelID {
			info.Tuning = "reconnecting…"
		}
	}
	return info
}

// tuningID is the channel on its way to playing: one waiting for a
// version-upgrade restart, one just asked for, or the one the server is
// connecting or reconnecting to. The list marks it so a channel change
// shows at once, while the old stream still plays and fades out.
func (m *Model) tuningID() string {
	switch {
	case m.pendingPlayID != "":
		return m.pendingPlayID
	case m.requestedID != "":
		return m.requestedID
	case m.Snapshot.Status == protocol.StatusConnecting, m.Snapshot.Status == protocol.StatusReconnecting:
		return m.Snapshot.ChannelID
	}
	return ""
}

// skewed reports whether the connected server runs a different version than the
// client, meaning the next channel change, pause or stop should restart it
// onto ours.
func (m *Model) skewed() bool {
	return m.ServerVersion != "" && client.VersionSkewed(m.About.Version, m.ServerVersion)
}

// playingOrConnecting reports whether the server is playing, or connecting
// to, channel id: the case in which it treats playing id as a no-op.
func (m *Model) playingOrConnecting(id string) bool {
	return id == m.Snapshot.ChannelID &&
		(m.Snapshot.Status == protocol.StatusPlaying || m.Snapshot.Status == protocol.StatusConnecting)
}

// applySnapshot installs a playback snapshot and derives the delegate's
// playing marker from it.
func (m *Model) applySnapshot(st protocol.PlaybackState) {
	m.Snapshot = st
	m.RequestErr = ""
	m.requestedID = ""
	if st.Status == protocol.StatusPlaying {
		m.PlayingID = st.ChannelID
	} else {
		m.PlayingID = ""
	}
}

// applyChannels installs a catalog payload: favorites, sorted items, stable
// selection, and the loading/error screens.
func (m *Model) applyChannels(payload protocol.ChannelsPayload) {
	if len(payload.Channels) == 0 {
		// The server had neither a cache nor a network catalog. Show its
		// error; an empty payload without one means the load is still
		// underway and a channels event will follow.
		if payload.Error != "" {
			m.Err = errors.New(payload.Error)
			m.Loading = false
		}
		return
	}

	firstLoad := m.Loading
	m.Err = nil
	m.RequestErr = ""
	m.Loading = false
	m.Favorites = payload.Favorites

	selectedID := m.selectedChannelID()
	m.allItems = m.sortItemsWithFavorites(ChannelsToItems(payload.Channels))

	if firstLoad && selectedID == "" {
		selectedID = payload.LastChannelID
	}
	// Recompute the visible (possibly filtered) list from the new catalog,
	// keeping the cursor on the same channel where possible.
	m.refreshVisibleItems(selectedID)
}

// matchCount is how many channels the active search query matches, or 0
// with no query.
func (m *Model) matchCount() int {
	if m.SearchQuery == "" {
		return 0
	}
	return len(m.List.Items())
}

// selectedChannelID returns the ID of the channel under the cursor, or ""
// when the list is empty. Callers capture it before re-sorting or
// re-filtering the list so refreshVisibleItems can keep the cursor there.
func (m *Model) selectedChannelID() string {
	if sel, ok := m.List.SelectedItem().(ui.Item); ok {
		return sel.Channel.ID
	}
	return ""
}

// selectChannelByID moves the list cursor to the channel with the given ID,
// if present, and reports whether it was found. Used to keep the selection
// stable across list re-sorts and re-filters.
func (m *Model) selectChannelByID(id string) bool {
	if id == "" {
		return false
	}
	for i, li := range m.List.Items() {
		if it, ok := li.(ui.Item); ok && it.Channel.ID == id {
			m.List.Select(i)
			return true
		}
	}
	return false
}

// volumeStep is how much the +/- keys change the volume.
const volumeStep = 0.05
