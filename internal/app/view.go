package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// RenderHeader renders the list header with column titles.
func (m *Model) RenderHeader() string {
	return ui.RenderHeader(m.List.Width(), m.FavoritesOnly)
}

// RenderSearchBar renders the search input bar.
func (m *Model) RenderSearchBar() string {
	n := m.matchCount()
	if m.Searching {
		matchInfo := ""
		if n > 0 {
			matchInfo = fmt.Sprintf(" [%d/%d]", m.List.Index()+1, n)
		} else if m.SearchQuery != "" {
			matchInfo = " [no matches]"
		}
		return ui.SearchBarStyle.Render(fmt.Sprintf("/%s%s", m.SearchQuery, matchInfo))
	}
	if m.SearchQuery != "" {
		matchInfo := ""
		if n > 0 {
			matchInfo = fmt.Sprintf(" [%d/%d] (n/N navigate, c clear)", m.List.Index()+1, n)
		}
		return ui.SearchBarStyle.Render(fmt.Sprintf("Search: %s%s", m.SearchQuery, matchInfo))
	}
	return ""
}

// RenderStatusBar renders the styled status bar from the latest server
// playback snapshot.
func (m *Model) RenderStatusBar() string {
	var icon, stateText string
	var stateStyle lipgloss.Style

	switch m.Snapshot.Status {
	case protocol.StatusConnecting:
		icon = "◌"
		stateText = "Connecting"
		stateStyle = ui.StatusConnectingStyle
	case protocol.StatusReconnecting:
		icon = "↻"
		stateText = fmt.Sprintf("Reconnecting #%d", m.Snapshot.ReconnectAttempt)
		stateStyle = ui.StatusConnectingStyle
	case protocol.StatusPlaying:
		icon = "▶"
		stateText = "Playing"
		stateStyle = ui.StatusPlayingStyle
	default:
		icon = "■"
		stateText = "Stopped"
		stateStyle = ui.StatusStoppedStyle
	}

	// Build the status line
	parts := []string{stateStyle.Render(icon + " " + stateText)}

	// Add the channel name if playing, connecting, or awaiting a reconnect
	if m.Snapshot.ChannelTitle != "" {
		channelStyle := lipgloss.NewStyle().Foreground(ui.TextColor)
		parts = append(parts, channelStyle.Render(m.Snapshot.ChannelTitle))
	}

	// Add track info with music note
	if m.Snapshot.TrackTitle != "" {
		trackStr := "♫ " + m.Snapshot.TrackTitle
		parts = append(parts, ui.TrackInfoStyle.Render(trackStr))
	}

	// Add stream error if present
	if m.Snapshot.StreamError != "" {
		errorStyle := lipgloss.NewStyle().Foreground(ui.ErrorColor)
		parts = append(parts, errorStyle.Render("Stream error: "+m.Snapshot.StreamError))
	}

	// Add the volume level
	volumeStyle := lipgloss.NewStyle().Foreground(ui.SubtleColor)
	parts = append(parts, volumeStyle.Render(fmt.Sprintf("♪ %d%%", int(math.Round(m.Snapshot.Volume*100)))))

	// Show a pending sleep timer (soma stop --in), if any.
	if label := sleepTimerLabel(m.Snapshot.StopAt); label != "" {
		parts = append(parts, volumeStyle.Render(label))
	}

	// Surface the last failed request until the server answers successfully.
	if m.RequestErr != "" {
		errorStyle := lipgloss.NewStyle().Foreground(ui.ErrorColor)
		parts = append(parts, errorStyle.Render(m.RequestErr))
	}

	// Surface connection trouble without hiding the list.
	if m.ServerLost {
		warnStyle := lipgloss.NewStyle().Foreground(ui.ErrorColor)
		parts = append(parts, warnStyle.Render("server connection lost — reconnecting…"))
	}

	style := ui.StatusBarStyle
	if m.Width > 0 {
		// Wrap on narrow terminals: the renderer truncates overlong lines,
		// which would clip exactly the errors this bar exists to show.
		// UpdateListSize measures the rendered height, so the list shrinks
		// to make room for the extra lines.
		style = style.Width(m.Width)
	}
	return style.Render(strings.Join(parts, "  │  "))
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

// RenderAboutFooter renders the about information as an inline footer, styled
// like the list help. It returns an empty string unless the about view is active.
func (m *Model) RenderAboutFooter() string {
	if !m.ShowAbout {
		return ""
	}
	return m.renderFooter([]string{
		fmt.Sprintf("Soma %s · commit %s · built %s", m.About.Version, m.About.Commit, m.About.Date),
		"A terminal UI for SomaFM internet radio · MIT License",
		"Author: Samuel Barabas · https://github.com/samuelb/somad",
		"Not affiliated with SomaFM. Streams provided by somafm.com.",
		"press a or esc to close",
	})
}

// RenderHistoryFooter renders the now-playing history for the playing
// channel as an inline footer, styled like the about footer. It shows only
// the newest entries that fit the window (see historyEntryLimit). It
// returns an empty string unless the history overlay is active.
func (m *Model) RenderHistoryFooter() string {
	if !m.ShowHistory {
		return ""
	}
	var lines []string
	switch {
	case m.HistoryChannelID == "":
		lines = append(lines, "History", "Nothing is playing.")
	case m.HistoryErr != nil:
		lines = append(lines, "History: "+m.HistoryChannelTitle,
			fmt.Sprintf("failed to load history: %v", m.HistoryErr))
	case len(m.History) == 0:
		lines = append(lines, "History: "+m.HistoryChannelTitle, "No history yet.")
	default:
		entries := m.History
		title := "History: " + m.HistoryChannelTitle
		if limit := m.historyEntryLimit(); len(entries) > limit {
			entries = entries[:limit]
			title += fmt.Sprintf(" (latest %d of %d)", limit, len(m.History))
		}
		lines = append(lines, title)
		for _, e := range entries {
			// One line per entry, so the limit above holds: a title can
			// carry line breaks from the stream.
			lines = append(lines, fmt.Sprintf("%s  %s", e.Time.Local().Format("15:04"), lineBreaks.Replace(e.Title)))
		}
	}
	lines = append(lines, "press h or esc to close")
	return m.renderFooter(lines)
}

// lineBreaks flattens line breaks to spaces.
var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

const (
	// historyFooterLines is how many lines the history footer takes
	// besides its entries: the separator, the title and the close hint.
	historyFooterLines = 3
	// minListHeight is the fewest lines the list renders in, however small
	// UpdateListSize sizes it: its status line and the blank under it, one
	// two-line row, the pagination line and the help line.
	minListHeight = 6
)

// historyEntryLimit is how many history entries fit on screen: the window
// height less the bottom margin, the rest of the chrome, the history
// footer's own lines and the list's minimum height. Before the window size
// is known there is no limit.
func (m *Model) historyEntryLimit() int {
	if m.Height <= 0 {
		return len(m.History)
	}
	used := 1 + historyFooterLines + minListHeight
	above, below := m.baseChrome()
	for _, c := range append(above, below...) {
		used += lipgloss.Height(c)
	}
	return max(0, m.Height-used)
}

// renderFooter renders lines as an inline footer below the status bar: a
// full-width separator, then the lines in the subtle help style.
func (m *Model) renderFooter(lines []string) string {
	width := m.List.Width()
	if width < 1 {
		width = m.Width
	}
	if width < 1 {
		width = 1
	}
	separator := ui.FooterSeparatorStyle.Render(strings.Repeat("─", width))
	body := ui.FooterBodyStyle.Render(strings.Join(lines, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, separator, body)
}

// View renders the application's UI.
func (m *Model) View() string {
	// Display loading message if channels are still being fetched
	if m.Loading {
		return ui.LoadingStyle.Render("◌ Loading SomaFM channels...")
	}

	// Display error message if channel loading failed
	if m.Err != nil {
		errorContent := fmt.Sprintf("✕ Error loading channels\n\n%v\n\nPress 'q' to quit", m.Err)
		return ui.ErrorBoxStyle.Render(errorContent)
	}

	above, below := m.chrome()
	components := append(append(above, m.List.View()), below...)
	return lipgloss.JoinVertical(lipgloss.Left, components...)
}

// chrome returns the rendered components that frame the list: those above
// it (top margin, header, search bar when active) and those below it
// (status bar, and the about and history footers when active). View joins
// them around the list; UpdateListSize measures them.
func (m *Model) chrome() (above, below []string) {
	above, below = m.baseChrome()
	if history := m.RenderHistoryFooter(); history != "" {
		below = append(below, history)
	}
	return above, below
}

// baseChrome is chrome without the history footer, the one component that
// sizes itself to the room the others leave.
func (m *Model) baseChrome() (above, below []string) {
	above = []string{"", m.RenderHeader()} // "" is the top margin line
	if searchBar := m.RenderSearchBar(); searchBar != "" {
		above = append(above, searchBar)
	}
	below = []string{m.RenderStatusBar()}
	if about := m.RenderAboutFooter(); about != "" {
		below = append(below, about)
	}
	return above, below
}

// UpdateListSize recalculates and sets the list size based on current UI state.
func (m *Model) UpdateListSize() {
	// Everything but the list itself, plus one bottom margin line.
	fixed := 1
	above, below := m.chrome()
	for _, c := range append(above, below...) {
		fixed += lipgloss.Height(c)
	}
	m.List.SetSize(m.Width, m.Height-fixed)
}

// ChannelsToItems converts channels to list items.
func ChannelsToItems(channels []channels.Channel) []list.Item {
	items := make([]list.Item, len(channels))
	for i, ch := range channels {
		items[i] = ui.Item{Channel: ch}
	}
	return items
}
