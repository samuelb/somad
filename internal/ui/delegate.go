package ui

import (
	"fmt"
	"io"

	"somatui/internal/channels"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Item implements the list.Item interface for displaying channels.
type Item struct {
	Channel channels.Channel
}

// Title returns the title of the channel for display in the list.
func (i Item) Title() string {
	return i.Channel.Title
}

// Description returns the description of the channel for display in the list.
func (i Item) Description() string { return i.Channel.Description }

// FilterValue returns the title of the channel for filtering purposes.
func (i Item) FilterValue() string { return i.Channel.Title }

// Listeners returns the listener count for display.
func (i Item) Listeners() string { return i.Channel.Listeners }

// StyledDelegate is a custom delegate for styling list items.
type StyledDelegate struct {
	list.DefaultDelegate
	PlayingID       *string
	MatchChecker    func(int) bool // Function to check if index is a search match
	FavoriteChecker func(int) bool // Function to check if index is a favorite
}

// NewStyledDelegate creates a styled delegate for the list.
func NewStyledDelegate(playingID *string, matchChecker func(int) bool, favoriteChecker func(int) bool) StyledDelegate {
	d := list.NewDefaultDelegate()

	// Normal item styles
	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Padding(0, 0, 0, 2)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(SubtleColor).
		Padding(0, 0, 0, 2)

	// Selected item styles
	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(PrimaryColor).
		Foreground(PrimaryColor).
		Bold(true).
		Padding(0, 0, 0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(PrimaryColor).
		Foreground(lipgloss.Color("#CCCCCC")).
		Padding(0, 0, 0, 1)

	return StyledDelegate{DefaultDelegate: d, PlayingID: playingID, MatchChecker: matchChecker, FavoriteChecker: favoriteChecker}
}

// Render renders a list item with custom styling, including a playing indicator.
func (d StyledDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(Item)
	if !ok {
		return
	}

	// Check if this item is currently playing
	isPlaying := d.PlayingID != nil && *d.PlayingID == i.Channel.ID
	isSelected := index == m.Index()
	isMatch := d.MatchChecker != nil && d.MatchChecker(index)
	isFavorite := d.FavoriteChecker != nil && d.FavoriteChecker(index)

	// Build title with playing/favorite indicator
	title := i.Title()
	if isFavorite {
		title = "♥ " + title
	}
	if isPlaying {
		title = "▶ " + title
	}

	// Calculate column widths
	leftColWidth, listenerColWidth := CalculateColumnWidths(m.Width())

	// Listener count styles
	listenerStyle := lipgloss.NewStyle().
		Foreground(SubtleColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerSelectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#CCCCCC")).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerPlayingStyle := lipgloss.NewStyle().
		Foreground(PlayingColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	listenerMatchStyle := lipgloss.NewStyle().
		Foreground(SearchMatchColor).
		Width(listenerColWidth).
		Align(lipgloss.Right)

	// Apply styles based on state
	var titleStr, descStr, listenerStr string
	listeners := i.Listeners() + " ♪"

	// Truncate description to prevent wrapping (content area is leftColWidth - 2 for padding)
	desc := ansi.Truncate(i.Description(), leftColWidth-2, "…")

	switch {
	case isSelected:
		// Subtract 1 from width to account for left border character
		titleStr = d.Styles.SelectedTitle.Width(leftColWidth - 1).Render(title)
		descStr = d.Styles.SelectedDesc.Width(leftColWidth - 1).Render(desc)
		listenerStr = listenerSelectedStyle.Render(listeners)
	case isPlaying:
		// Playing but not selected - show green indicator
		playingTitleStyle := lipgloss.NewStyle().
			Foreground(PlayingColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		playingDescStyle := lipgloss.NewStyle().
			Foreground(SubtleColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		titleStr = playingTitleStyle.Render(title)
		descStr = playingDescStyle.Render(desc)
		listenerStr = listenerPlayingStyle.Render(listeners)
	case isMatch:
		// Search match - highlight with match color
		matchTitleStyle := lipgloss.NewStyle().
			Foreground(SearchMatchColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		matchDescStyle := lipgloss.NewStyle().
			Foreground(SubtleColor).
			Padding(0, 0, 0, 2).
			Width(leftColWidth)
		titleStr = matchTitleStyle.Render(title)
		descStr = matchDescStyle.Render(desc)
		listenerStr = listenerMatchStyle.Render(listeners)
	default:
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

const (
	listenerColumnWidth = 12
	minLeftColumnWidth  = 20
)

// CalculateColumnWidths returns the left and listener column widths for a given total width.
func CalculateColumnWidths(totalWidth int) (leftCol, listenerCol int) {
	listenerCol = listenerColumnWidth
	leftCol = totalWidth - listenerCol - 4
	if leftCol < minLeftColumnWidth {
		leftCol = minLeftColumnWidth
	}
	return
}
