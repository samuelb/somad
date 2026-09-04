package ui

import (
	"fmt"
	"io"

	"somad/internal/channels"

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
		Foreground(TextColor).
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
		Foreground(MutedTextColor).
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

	leftColWidth, listenerColWidth := CalculateColumnWidths(m.Width())
	listeners := i.Listeners() + " ♪"
	// Truncate description to prevent wrapping (content area is leftColWidth - 2 for padding)
	desc := ansi.Truncate(i.Description(), leftColWidth-2, "…")

	// Pick the (title, description, listener count) styles for the row's state.
	var titleStyle, descStyle, listenerStyle lipgloss.Style
	switch {
	case isSelected:
		// Subtract 1 from width to account for left border character
		titleStyle = d.Styles.SelectedTitle.Width(leftColWidth - 1)
		descStyle = d.Styles.SelectedDesc.Width(leftColWidth - 1)
		listenerStyle = listenerSelectedStyle
	case isPlaying:
		// Playing but not selected - show green indicator
		titleStyle = playingTitleStyle.Width(leftColWidth)
		descStyle = unselectedDescStyle.Width(leftColWidth)
		listenerStyle = listenerPlayingStyle
	case isMatch:
		// Search match - highlight with match color
		titleStyle = matchTitleStyle.Width(leftColWidth)
		descStyle = unselectedDescStyle.Width(leftColWidth)
		listenerStyle = listenerMatchStyle
	default:
		titleStyle = d.Styles.NormalTitle.Width(leftColWidth)
		descStyle = d.Styles.NormalDesc.Width(leftColWidth)
		listenerStyle = listenerNormalStyle
	}
	titleStr := titleStyle.Render(title)
	descStr := descStyle.Render(desc)
	listenerStr := listenerStyle.Width(listenerColWidth).Render(listeners)

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

// RenderHeader renders the list header with column titles, aligned to the
// same columns the delegate renders rows in.
func RenderHeader(width int, favoritesOnly bool) string {
	leftColWidth, listenerColWidth := CalculateColumnWidths(width)
	titleText := "SomaFM Stations"
	if favoritesOnly {
		titleText += " · Favorites"
	}
	title := TitleStyle.Width(leftColWidth).Render(titleText)
	listenerHeader := listenerNormalStyle.Width(listenerColWidth).Render("Listeners")
	return lipgloss.JoinHorizontal(lipgloss.Bottom, title, listenerHeader)
}

// CalculateColumnWidths returns the left and listener column widths for a given total width.
func CalculateColumnWidths(totalWidth int) (leftCol, listenerCol int) {
	listenerCol = listenerColumnWidth
	leftCol = totalWidth - listenerCol - 4
	if leftCol < minLeftColumnWidth {
		leftCol = minLeftColumnWidth
	}
	return
}
