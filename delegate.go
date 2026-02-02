package main

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
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
	playingIndex *int
}

// newStyledDelegate creates a styled delegate for the list.
func newStyledDelegate(playingIndex *int) styledDelegate {
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

	return styledDelegate{DefaultDelegate: d, playingIndex: playingIndex}
}

// Render renders a list item with custom styling, including a playing indicator.
func (d styledDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(item)
	if !ok {
		return
	}

	// Check if this item is currently playing
	isPlaying := d.playingIndex != nil && *d.playingIndex == index
	isSelected := index == m.Index()

	// Build title with playing indicator
	title := i.Title()
	if isPlaying {
		title = "▶ " + title
	}

	// Calculate column widths
	listWidth := m.Width()
	listenerColWidth := 12                           // Space for "XXX listeners"
	leftColWidth := listWidth - listenerColWidth - 4 // 4 for padding/margins

	if leftColWidth < 20 {
		leftColWidth = 20
	}

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

	// Apply styles based on state
	var titleStr, descStr, listenerStr string
	listeners := i.Listeners() + " ♪"

	if isSelected {
		titleStr = d.Styles.SelectedTitle.Copy().Width(leftColWidth).Render(title)
		descStr = d.Styles.SelectedDesc.Copy().Width(leftColWidth).Render(i.Description())
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
		descStr = playingDescStyle.Render(i.Description())
		listenerStr = listenerPlayingStyle.Render(listeners)
	} else {
		titleStr = d.Styles.NormalTitle.Copy().Width(leftColWidth).Render(title)
		descStr = d.Styles.NormalDesc.Copy().Width(leftColWidth).Render(i.Description())
		listenerStr = listenerStyle.Render(listeners)
	}

	// Build two-column layout
	// Title row with listener count
	titleRow := lipgloss.JoinHorizontal(lipgloss.Top, titleStr, listenerStr)
	// Description row (no listener count, just padding to align)
	descRow := descStr

	fmt.Fprintf(w, "%s\n%s", titleRow, descRow)
}
