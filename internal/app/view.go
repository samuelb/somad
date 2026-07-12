package app

import (
	"fmt"
	"math"
	"strings"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// RenderHeader renders the list header with column titles.
func (m *Model) RenderHeader() string {
	leftColWidth, listenerColWidth := ui.CalculateColumnWidths(m.List.Width())

	title := ui.TitleStyle.Width(leftColWidth).Render("SomaFM Stations")
	listenerHeader := lipgloss.NewStyle().
		Foreground(ui.SubtleColor).
		Width(listenerColWidth).
		Align(lipgloss.Right).
		Render("Listeners")

	return lipgloss.JoinHorizontal(lipgloss.Bottom, title, listenerHeader)
}

// RenderSearchBar renders the search input bar.
func (m *Model) RenderSearchBar() string {
	if m.Searching {
		matchInfo := ""
		if len(m.SearchMatches) > 0 {
			matchInfo = fmt.Sprintf(" [%d/%d]", m.CurrentMatch+1, len(m.SearchMatches))
		} else if m.SearchQuery != "" {
			matchInfo = " [no matches]"
		}
		return ui.SearchBarStyle.Render(fmt.Sprintf("/%s%s", m.SearchQuery, matchInfo))
	}
	if m.SearchQuery != "" {
		matchInfo := ""
		if len(m.SearchMatches) > 0 {
			matchInfo = fmt.Sprintf(" [%d/%d] (n/N navigate, c clear)", m.CurrentMatch+1, len(m.SearchMatches))
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
		channelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
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

// RenderAboutFooter renders the about information as an inline footer, styled
// like the list help. It returns an empty string unless the about view is active.
func (m *Model) RenderAboutFooter() string {
	if !m.ShowAbout {
		return ""
	}

	width := m.List.Width()
	if width < 1 {
		width = m.Width
	}
	if width < 1 {
		width = 1
	}

	separator := lipgloss.NewStyle().
		Foreground(ui.SubtleColor).
		Render(strings.Repeat("─", width))

	lines := []string{
		fmt.Sprintf("Soma %s · commit %s · built %s", m.About.Version, m.About.Commit, m.About.Date),
		"A terminal UI for SomaFM internet radio · MIT License",
		"Author: Samuel Barabas · https://github.com/samuelb/somad",
		"Not affiliated with SomaFM. Streams provided by somafm.com.",
		"press a or esc to close",
	}

	body := lipgloss.NewStyle().
		Foreground(ui.SubtleColor).
		Padding(0, 0, 0, 2).
		Render(strings.Join(lines, "\n"))

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

	// Build the main view using lipgloss layout
	components := []string{
		"", // Top margin
		m.RenderHeader(),
	}
	if searchBar := m.RenderSearchBar(); searchBar != "" {
		components = append(components, searchBar)
	}
	components = append(components, m.List.View(), m.RenderStatusBar())

	// Show the about information as an inline footer when active.
	if about := m.RenderAboutFooter(); about != "" {
		components = append(components, about)
	}

	return lipgloss.JoinVertical(lipgloss.Left, components...)
}

// UpdateListSize recalculates and sets the list size based on current UI state.
func (m *Model) UpdateListSize() {
	// Dynamically calculate the height needed for the header and status bar
	headerHeight := lipgloss.Height(m.RenderHeader())
	statusBarHeight := lipgloss.Height(m.RenderStatusBar())
	searchBarHeight := 0
	if searchBar := m.RenderSearchBar(); searchBar != "" {
		searchBarHeight = lipgloss.Height(searchBar)
	}
	aboutHeight := 0
	if about := m.RenderAboutFooter(); about != "" {
		aboutHeight = lipgloss.Height(about)
	}

	// Total height occupied by elements other than the list itself
	totalFixedUIHeight := 1 + headerHeight + searchBarHeight + statusBarHeight + aboutHeight + 1

	// Update the list's dimensions
	m.List.SetSize(m.Width, m.Height-totalFixedUIHeight)
}

// ChannelsToItems converts channels to list items.
func ChannelsToItems(channels []channels.Channel) []list.Item {
	items := make([]list.Item, len(channels))
	for i, ch := range channels {
		items[i] = ui.Item{Channel: ch}
	}
	return items
}
