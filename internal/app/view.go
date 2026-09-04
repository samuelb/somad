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
// no timer is pending or the timestamp cannot be parsed.
func sleepTimerLabel(stopAt string) string {
	if stopAt == "" {
		return ""
	}
	at, err := time.Parse(time.RFC3339, stopAt)
	if err != nil {
		return ""
	}
	return formatSleepRemaining(time.Until(at))
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
// channel as an inline footer, styled like the about footer. It returns an
// empty string unless the history overlay is active.
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
		lines = append(lines, "History: "+m.HistoryChannelTitle)
		for _, e := range m.History {
			lines = append(lines, fmt.Sprintf("%s  %s", e.Time.Local().Format("15:04"), e.Title))
		}
	}
	lines = append(lines, "press h or esc to close")
	return m.renderFooter(lines)
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
	above = []string{"", m.RenderHeader()} // "" is the top margin line
	if searchBar := m.RenderSearchBar(); searchBar != "" {
		above = append(above, searchBar)
	}
	below = []string{m.RenderStatusBar()}
	for _, footer := range []string{m.RenderAboutFooter(), m.RenderHistoryFooter()} {
		if footer != "" {
			below = append(below, footer)
		}
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
