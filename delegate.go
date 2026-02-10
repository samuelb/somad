package main

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// item implements the list.Item interface for displaying channels.
type item struct {
	channel Channel
}

// Title returns the title of the channel for display in the list.
func (i item) Title() string {
	return i.channel.Title
}

// Description returns the description of the channel for display in the list.
func (i item) Description() string { return i.channel.Description }

// FilterValue returns the title of the channel for filtering purposes.
func (i item) FilterValue() string { return i.channel.Title }

// Listeners returns the listener count for display.
func (i item) Listeners() string { return i.channel.Listeners }

// styledDelegate is a custom delegate for styling list items.
type styledDelegate struct {
	list.DefaultDelegate
	playingID       *string
	matchChecker    func(int) bool // Function to check if index is a search match
	favoriteChecker func(int) bool // Function to check if index is a favorite
}

// newStyledDelegate creates a styled delegate for the list.
func newStyledDelegate(playingID *string, matchChecker func(int) bool, favoriteChecker func(int) bool) styledDelegate {
	d := list.NewDefaultDelegate()

	// Normal item styles
	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(subtleColor).
		Padding(0, 0, 0, 2)

	// Selected item styles
	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(primaryColor).
		Foreground(primaryColor).
		Bold(true).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(primaryColor).
		Foreground(lipgloss.Color("#CCCCCC")).
		Padding(0, 0, 0, 1)

	return styledDelegate{DefaultDelegate: d, playingID: playingID, matchChecker: matchChecker, favoriteChecker: favoriteChecker}
}

// Render renders a list item with custom styling, including a playing indicator.
func (d styledDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	// Check if this item is currently playing
	isPlaying := d.playingID != nil && *d.playingID == i.channel.ID
	isSelected := index == m.Index()
	isMatch := d.matchChecker != nil && d.matchChecker(index)
	isFavorite := d.favoriteChecker != nil && d.favoriteChecker(index)

	// Build title with playing/favorite indicator
	title := i.Title()
	if isFavorite {
		title = "★ " + title
	}
	if isPlaying {
		title = "▶ " + title
	}

	// Calculate column widths
	leftColWidth, listenerColWidth := calculateColumnWidths(m.Width())

	// Listener count styles
	listenerStyle := lipgloss.NewStyle().
		Foreground(subtleColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerSelectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CCCCCC")).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerPlayingStyle := lipgloss.NewStyle().
		Foreground(playingColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerMatchStyle := lipgloss.NewStyle().
		Foreground(searchMatchColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerFavoriteStyle := lipgloss.NewStyle().
		Foreground(favoriteColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	// Apply styles based on state
	var titleStr, descStr, listenerStr string
	listeners := i.Listeners() + " ♪"

	// Truncate description to prevent wrapping (content area is leftColWidth - 2 for padding)
	desc := ansi.Truncate(i.Description(), leftColWidth-2, "…")

	if isSelected {
		// Subtract 1 from width to account for left border character
		titleStr = d.Styles.SelectedTitle.Width(leftColWidth - 1).Render(title)
		descStr = d.Styles.SelectedDesc.Width(leftColWidth - 1).Render(desc)
		listenerStr = listenerSelectedStyle.Render(listeners)
	} else if isPlaying {
		// Playing but not selected - show green indicator
		playingTitleStyle := lipgloss.NewStyle().
			Foreground(playingColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		playingDescStyle := lipgloss.NewStyle().
			Foreground(subtleColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		titleStr = playingTitleStyle.Render(title)
		descStr = playingDescStyle.Render(desc)
		listenerStr = listenerPlayingStyle.Render(listeners)
	} else if isMatch {
		// Search match - highlight with match color
		matchTitleStyle := lipgloss.NewStyle().
			Foreground(searchMatchColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		matchDescStyle := lipgloss.NewStyle().
			Foreground(subtleColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		titleStr = matchTitleStyle.Render(title)
		descStr = matchDescStyle.Render(desc)
		listenerStr = listenerMatchStyle.Render(listeners)
	} else if isFavorite {
		// Favorite - highlight with favorite color
		favoriteTitleStyle := lipgloss.NewStyle().
			Foreground(favoriteColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		favoriteDescStyle := lipgloss.NewStyle().
			Foreground(subtleColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		titleStr = favoriteTitleStyle.Render(title)
		descStr = favoriteDescStyle.Render(desc)
		listenerStr = listenerFavoriteStyle.Render(listeners)
	} else {
		titleStr = d.Styles.NormalTitle.Width(leftColWidth).Render(title)
		descStr = d.Styles.NormalDesc.Width(leftColWidth).Render(desc)
		listenerStr = listenerStyle.Render(listeners)
	}

	// Build two-column layout
	// Title row with listener count
	titleRow := lipgloss.JoinHorizontal(lipgloss.Top, titleStr, listenerStr)
	// Description row (no listener count, just padding to align)
	descRow := descStr

	_, _ = fmt.Fprintf(w, "%s\n%s", titleRow, descRow)
}
