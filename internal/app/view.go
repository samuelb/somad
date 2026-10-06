package app

import (
	"fmt"
	"strings"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// screenWidth is the width to lay the view out in: the window's, or before
// the first size message, the list's or a conventional 80 columns.
func (m *Model) screenWidth() int {
	switch {
	case m.Width > 0:
		return m.Width
	case m.List.Width() > 0:
		return m.List.Width()
	default:
		return 80
	}
}

// cardWidth is the width of the cards under the list: the screen less a
// margin column at each side, so their borders line up with the selection
// bar on the left and the listener column on the right.
func (m *Model) cardWidth() int {
	return max(m.screenWidth()-2, 8)
}

// card renders lines in a ui.Card of cardWidth, indented by the margin.
func (m *Model) card(border lipgloss.TerminalColor, title, info string, lines []string) string {
	c := ui.Card(m.cardWidth(), border, title, info, strings.Join(lines, "\n"))
	return lipgloss.NewStyle().MarginLeft(1).Render(c)
}

// RenderHeader renders the title line above the list.
func (m *Model) RenderHeader() string {
	count := len(m.allItems)
	if m.FavoritesOnly {
		count = len(filterItems(m.allItems, func(i ui.Item) bool { return m.isFavoriteID(i.Channel.ID) }))
	}
	return ui.RenderHeader(m.screenWidth(), m.FavoritesOnly, count)
}

// RenderSearchBar renders the search line under the header: the query
// (with a cursor while typing) and where the cursor is among the matches.
// It returns an empty string when no search is active.
func (m *Model) RenderSearchBar() string {
	if !m.Searching && m.SearchQuery == "" {
		return ""
	}
	left := ui.SearchPromptStyle.Render("/") + " " + ui.SearchQueryStyle.Render(m.SearchQuery)
	if m.Searching {
		left += ui.SearchCursorStyle.Render(" ")
	}

	n := m.matchCount()
	var right string
	switch {
	case m.SearchQuery == "":
		right = ui.SubtleStyle.Render("type to filter · enter keeps the filter · esc cancels")
	case n == 0:
		right = ui.ErrorStyle.Render("no matches")
	case m.Searching:
		right = ui.MutedStyle.Render(fmt.Sprintf("%d of %d", m.List.Index()+1, n))
	default:
		right = ui.MutedStyle.Render(fmt.Sprintf("%d of %d", m.List.Index()+1, n)) +
			ui.SubtleStyle.Render(" · n/N navigate · c clear")
	}
	// The prompt sits in the selection bar's column, the query in the
	// titles'; the match count ends with the listener column.
	return " " + ui.SpaceBetween(m.screenWidth()-2, left, right)
}

// RenderNowPlaying renders the now-playing card from the latest server
// playback snapshot: the state in the border (colored to match, with a
// spinner while connecting), the channel, the track, the volume, a pending
// sleep timer, and any errors to surface.
func (m *Model) RenderNowPlaying() string {
	st := m.Snapshot
	width := ui.CardContentWidth(m.cardWidth())
	spinner := ui.Spinner(m.frame)

	border := lipgloss.TerminalColor(ui.BorderColor)
	accent := lipgloss.TerminalColor(ui.PrimaryColor)
	var title, hint string
	switch st.Status {
	case protocol.StatusConnecting:
		border = ui.PrimaryColor
		title = ui.StatusConnectingStyle.Render(spinner + " Connecting")
		hint = "Tuning in…"
	case protocol.StatusReconnecting:
		border = ui.PrimaryColor
		title = ui.StatusConnectingStyle.Render(fmt.Sprintf("%s Reconnecting #%d", spinner, st.ReconnectAttempt))
		hint = "The stream dropped; reconnecting…"
	case protocol.StatusPlaying:
		border, accent = ui.PlayingColor, ui.PlayingColor
		title = ui.StatusPlayingStyle.Render("▶ Now Playing")
		hint = "Waiting for the track title…"
	default:
		title = ui.StatusStoppedStyle.Render("■ Stopped")
		hint = "Pick a station and press enter to tune in"
	}
	if m.ServerLost {
		// The snapshot is stale until the reconnect delivers a fresh one.
		border = ui.ErrorColor
		title = ui.StatusErrorStyle.Render(spinner + " Disconnected")
	}

	channel := ui.SubtleStyle.Render("Nothing playing")
	if st.ChannelTitle != "" {
		channel = ui.BoldStyle.Render(st.ChannelTitle)
	}
	gauge := ui.VolumeGauge(st.Volume, volumeBarWidth(width), accent)
	lines := []string{ui.SpaceBetween(width, channel, gauge)}

	second := ui.SubtleStyle.Render(hint)
	if st.TrackTitle != "" {
		eq := ui.EqualizerRest
		if m.eqPlays() {
			eq = ui.Equalizer(m.frame)
		}
		second = ui.StatusPlayingStyle.Render(eq+" ") + trackLine(st.TrackTitle, ui.BoldStyle)
	}
	lines = append(lines, ansi.Truncate(second, width, "…"))

	// Errors wrap rather than truncate: the renderer clips overlong lines,
	// which would cut off exactly what these exist to show. UpdateListSize
	// measures the card, so the list shrinks to make room.
	wrapErr := ui.ErrorStyle.Width(width)
	if st.StreamError != "" {
		lines = append(lines, wrapErr.Render("✕ Stream error: "+st.StreamError))
	}
	if m.RequestErr != "" {
		lines = append(lines, wrapErr.Render("✕ "+m.RequestErr))
	}
	if m.ServerLost {
		lines = append(lines, wrapErr.Render("✕ server connection lost — reconnecting…"))
	}

	// A pending sleep timer (soma stop --in) sits in the top border, and
	// for a moment, the visualizer style just picked with v.
	var notes []string
	if m.vizNotice {
		notes = append(notes, ui.MutedStyle.Render("visualizer: "+m.Visualizer.String()))
	}
	if label := sleepTimerLabel(st.StopAt); label != "" {
		notes = append(notes, ui.AccentStyle.Render("☾ "+label))
	}
	info := strings.Join(notes, ui.SubtleStyle.Render(" · "))
	return m.card(border, title, info, lines)
}

// volumeBarWidth is how wide a volume slider fits a now-playing line width
// cells wide; 0 leaves just the percentage.
func volumeBarWidth(width int) int {
	switch {
	case width >= 64:
		return 16
	case width >= 48:
		return 10
	default:
		return 0
	}
}

// trackLine renders a raw ICY stream title: "Artist — Title" with the
// title in titleStyle when it splits that way (audio.SplitTitle, the split
// MPRIS and Last.fm use), else the whole title as it came.
func trackLine(raw string, titleStyle lipgloss.Style) string {
	artist, title := audio.SplitTitle(lineBreaks.Replace(raw))
	if artist == "" {
		return ui.TrackInfoStyle.Render(title)
	}
	return ui.MutedStyle.Render(artist) + ui.SubtleStyle.Render(" — ") + titleStyle.Render(title)
}

// sleepTimerLabel renders the pending sleep-timer stop (protocol.
// PlaybackState.StopAt, an RFC 3339 timestamp) as "sleep in 42m", or "" when
// no timer is pending or the timestamp cannot be parsed. Nothing else
// re-renders while playback is quiet, so syncSleepTick keeps it current.
func sleepTimerLabel(stopAt string) string {
	at, ok := parseStopAt(stopAt)
	if !ok {
		return ""
	}
	return formatSleepRemaining(time.Until(at))
}

// parseStopAt parses a protocol.PlaybackState.StopAt timestamp, reporting
// false when no timer is pending or it cannot be parsed.
func parseStopAt(stopAt string) (time.Time, bool) {
	if stopAt == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, stopAt)
	return at, err == nil
}

// sleepTickSlack is how far past a label change sleepTickDelay aims, so
// the tick lands after the change rather than on it.
const sleepTickSlack = 10 * time.Millisecond

// sleepTickDelay returns how long until the formatSleepRemaining label for
// a remaining d next changes, or false once it reads 0s and cannot change
// again. The label rounds to the nearest minute, or second in the last
// minute, so it changes half a unit below the value it shows (and at the
// one-minute mark, where it switches to seconds).
func sleepTickDelay(d time.Duration) (time.Duration, bool) {
	unit, floor := time.Second, time.Duration(0)
	if d >= time.Minute {
		unit, floor = time.Minute, time.Minute
	}
	shown := d.Round(unit)
	if shown <= 0 {
		return 0, false
	}
	next := max(shown-unit/2, floor)
	return d - next + sleepTickSlack, true
}

// formatSleepRemaining renders a duration until a pending sleep-timer stop
// as "sleep in Xm" (or "sleep in Xs" once under a minute); a negative
// duration (the timer is about to fire) renders as "sleep in 0s".
func formatSleepRemaining(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("sleep in %ds", int(d.Round(time.Second).Seconds()))
	}
	return fmt.Sprintf("sleep in %dm", int(d.Round(time.Minute).Minutes()))
}

// closeHint is the top-edge hint of a card closed by key k or esc.
func closeHint(k string) string {
	return ui.HelpKeyStyle.Render(k) + ui.HelpDescStyle.Render(" or ") +
		ui.HelpKeyStyle.Render("esc") + ui.HelpDescStyle.Render(" to close")
}

// cardTitleStyle renders the titles of the about and history cards.
var cardTitleStyle = ui.AccentStyle.Bold(true)

// RenderAbout renders the about card, shown above the now-playing card. It
// returns an empty string unless the about view is active.
func (m *Model) RenderAbout() string {
	if !m.ShowAbout {
		return ""
	}
	width := ui.CardContentWidth(m.cardWidth())
	lines := []string{
		ui.BoldStyle.Render("Soma "+m.About.Version) +
			ui.SubtleStyle.Render(fmt.Sprintf(" · commit %s · built %s", m.About.Commit, m.About.Date)),
		ui.MutedStyle.Render("A terminal UI for SomaFM internet radio · MIT License"),
		ui.MutedStyle.Render("Author: Samuel Barabas · https://github.com/samuelb/somad"),
		ui.SubtleStyle.Render("Not affiliated with SomaFM. Streams provided by somafm.com."),
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width, "…")
	}
	return m.card(ui.BorderColor, cardTitleStyle.Render("About"), closeHint("a"), lines)
}

// RenderHistory renders the now-playing history for the playing channel as
// a card above the now-playing card. It shows only the newest entries that
// fit the window (see historyEntryLimit). It returns an empty string unless
// the history overlay is active.
func (m *Model) RenderHistory() string {
	if !m.ShowHistory {
		return ""
	}
	width := ui.CardContentWidth(m.cardWidth())
	title := cardTitleStyle.Render("History")
	if m.HistoryChannelTitle != "" {
		title += ui.SubtleStyle.Render(" · ") + ui.MutedStyle.Render(m.HistoryChannelTitle)
	}
	var lines []string
	switch {
	case m.HistoryChannelID == "":
		lines = append(lines, ui.SubtleStyle.Render("Nothing is playing."))
	case m.HistoryErr != nil:
		lines = append(lines, ui.ErrorStyle.Width(width).Render(fmt.Sprintf("failed to load history: %v", m.HistoryErr)))
	case len(m.History) == 0:
		lines = append(lines, ui.SubtleStyle.Render("No history yet."))
	default:
		entries := m.History
		if limit := m.historyEntryLimit(); len(entries) > limit {
			entries = entries[:limit]
			title += ui.SubtleStyle.Render(fmt.Sprintf(" · latest %d of %d", limit, len(m.History)))
		}
		for _, e := range entries {
			// One line per entry, so the limit above holds: a title can
			// carry line breaks from the stream, and may be too long.
			line := ui.SubtleStyle.Render(e.Time.Local().Format("15:04")) + "  " + trackLine(e.Title, ui.TextStyle)
			lines = append(lines, ansi.Truncate(line, width, "…"))
		}
	}
	return m.card(ui.BorderColor, title, closeHint("h"), lines)
}

// lineBreaks flattens line breaks to spaces.
var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

const (
	// historyCardLines is how many lines the history card takes besides
	// its entries: its top and bottom borders.
	historyCardLines = 2
	// minListHeight is the fewest lines the list renders in, however small
	// UpdateListSize sizes it: one two-line row and the pagination line.
	minListHeight = 3
	// helpIndent lines the help up with the row titles, and
	// paginationIndent the pagination dots, each dotWidth cells wide.
	helpIndent       = 3
	paginationIndent = 3
	dotWidth         = 2
)

// historyEntryLimit is how many history entries fit on screen: the window
// height less the bottom margin, the rest of the chrome, the history
// card's own lines and the list's minimum height. Before the window size
// is known there is no limit.
func (m *Model) historyEntryLimit() int {
	if m.Height <= 0 {
		return len(m.History)
	}
	used := 1 + historyCardLines + minListHeight + chromeHeight(m.baseChrome())
	return max(0, m.Height-used)
}

// RenderHelp renders the key help under the now-playing card: one line of
// the main keys, or every key once ? expands it.
func (m *Model) RenderHelp() string {
	width := max(m.screenWidth()-helpIndent-1, 0)
	var help string
	if m.List.Help.ShowAll {
		h := m.List.Help
		h.Width = 0 // it can overflow anyway; MaxWidth cuts the columns instead
		help = lipgloss.NewStyle().MaxWidth(width).Render(h.FullHelpView(m.List.FullHelp()))
	} else {
		help = ui.ShortHelp(m.List.ShortHelp(), width)
	}
	return lipgloss.NewStyle().PaddingLeft(helpIndent).Render(help)
}

// View renders the application's UI.
func (m *Model) View() string {
	// Display a spinner while channels are still being fetched
	if m.Loading {
		return m.place(lipgloss.JoinVertical(lipgloss.Center,
			ui.TitleStyle.Render("SomaFM"),
			"",
			ui.SpinnerStyle.Render(ui.Spinner(m.frame))+" "+ui.LoadingStyle.Render("Loading channels…"),
		))
	}

	// Display error message if channel loading failed
	if m.Err != nil {
		width := min(max(m.screenWidth()-10, 20), 64)
		box := ui.ErrorBoxStyle.Render(lipgloss.JoinVertical(lipgloss.Left,
			ui.StatusErrorStyle.Render("✕ Error loading channels"),
			"",
			ui.TextStyle.Width(width).Render(m.Err.Error()),
			"",
			ui.HelpKeyStyle.Render("q")+ui.HelpDescStyle.Render(" to quit"),
		))
		return m.place(box)
	}

	body := m.List.View()
	if len(m.List.Items()) == 0 {
		body = m.renderEmptyList()
	}
	above, below := m.chrome()
	components := append(append(above, body), below...)
	view := lipgloss.JoinVertical(lipgloss.Left, components...)
	if m.Visualizer != ui.VisualizerOff && m.Width > 0 {
		// The bars rise from the top of the cards, which stay opaque.
		field := chromeHeight(above, nil) + lipgloss.Height(body)
		for _, c := range below {
			if c != "" {
				break
			}
			field++ // a spacing line
		}
		view = m.viz.Underlay(view, m.Visualizer, m.Width, field)
	}
	return view
}

// place centers s in the window, or returns it as is before the window
// size is known.
func (m *Model) place(s string) string {
	if m.Width <= 0 || m.Height <= 0 {
		return s
	}
	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, s)
}

// renderEmptyList renders what stands in for the list when it has nothing
// to show, centered in the list's room: why, and how to get channels back.
func (m *Model) renderEmptyList() string {
	var head, hint string
	switch {
	case m.SearchQuery != "":
		head = fmt.Sprintf("No stations match “%s”", m.SearchQuery)
		if m.FavoritesOnly {
			head = fmt.Sprintf("No favorites match “%s”", m.SearchQuery)
		}
		hint = "esc clears the search"
	case m.FavoritesOnly:
		head = ui.FavoriteStyle.Render("♥") + " No favorites yet"
		hint = "F shows all stations; f marks the selected one as a favorite"
	default:
		head = "No stations"
	}
	msg := lipgloss.JoinVertical(lipgloss.Center, ui.MutedStyle.Render(head), "", ui.SubtleStyle.Render(hint))
	return lipgloss.Place(m.screenWidth(), max(m.List.Height(), 1), lipgloss.Center, lipgloss.Center, msg)
}

// chrome returns the rendered components that frame the list: those above
// it (top margin, header, search bar when active, and a blank line) and
// those below it (a blank line, the about and history cards when active,
// the now-playing card, and the help). View joins them around the list; UpdateListSize
// measures them.
func (m *Model) chrome() (above, below []string) {
	return m.chromeWith(m.RenderHistory())
}

// baseChrome is chrome without the history card, the one component that
// sizes itself to the room the others leave.
func (m *Model) baseChrome() (above, below []string) {
	return m.chromeWith("")
}

// chromeWith is chrome with the given, already rendered, history card. In
// a window too short to leave the list minListHeight, the spacing lines go
// first, then the short help; the full help stays, having been asked for.
func (m *Model) chromeWith(history string) (above, below []string) {
	for _, tight := range []chromeLevel{roomy, unspaced, bare} {
		above, below = m.layout(history, tight)
		if m.Height <= 0 || chromeHeight(above, below)+1+minListHeight <= m.Height {
			break
		}
	}
	return above, below
}

// chromeLevel is how much of the optional chrome a layout keeps.
type chromeLevel int

const (
	roomy    chromeLevel = iota // everything
	unspaced                    // no blank spacing lines
	bare                        // no spacing and no short help either
)

// layout returns the chrome at the given level; see chromeWith.
func (m *Model) layout(history string, level chromeLevel) (above, below []string) {
	spaced := level == roomy
	if spaced {
		above = append(above, "") // the top margin line
	}
	above = append(above, m.RenderHeader())
	if searchBar := m.RenderSearchBar(); searchBar != "" {
		above = append(above, searchBar)
	}
	if spaced {
		above = append(above, "")
		below = append(below, "") // a blank line between the list and the cards
	}
	if about := m.RenderAbout(); about != "" {
		below = append(below, about)
	}
	if history != "" {
		below = append(below, history)
	}
	below = append(below, m.RenderNowPlaying())
	if level != bare || m.List.Help.ShowAll {
		below = append(below, m.RenderHelp())
	}
	return above, below
}

// chromeHeight is how many lines the components take.
func chromeHeight(above, below []string) int {
	n := 0
	for _, c := range append(above, below...) {
		n += lipgloss.Height(c)
	}
	return n
}

// UpdateListSize recalculates and sets the list size based on current UI state.
func (m *Model) UpdateListSize() {
	// Everything but the list itself, plus one bottom margin line.
	fixed := 1 + chromeHeight(m.chrome())
	m.List.SetSize(m.Width, m.Height-fixed)
	// The list switches to page numbers only when the bare dots overflow;
	// ours are indented and spaced, so decide here.
	m.List.Paginator.Type = paginator.Dots
	if paginationIndent+dotWidth*m.List.Paginator.TotalPages > m.Width {
		m.List.Paginator.Type = paginator.Arabic
	}
}

// ChannelsToItems converts channels to list items.
func ChannelsToItems(channels []channels.Channel) []list.Item {
	items := make([]list.Item, len(channels))
	for i, ch := range channels {
		items[i] = ui.Item{Channel: ch}
	}
	return items
}
