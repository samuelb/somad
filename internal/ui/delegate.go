package ui

import (
	"fmt"
	"io"
	"strings"

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

// RowInfo is what a row shows beyond its channel's own data.
type RowInfo struct {
	// Playing marks the channel the server is playing.
	Playing bool
	// Tuning, when set, is shown with a spinner after the title in place
	// of the genre: the channel was just asked to play, or is connecting
	// or reconnecting, and is not playing yet.
	Tuning string
	// Favorite marks a favorite channel.
	Favorite bool
	// TitleMatches and DescMatches are the rune indexes of the active
	// search query's match in the title or description; nil when the row
	// is not a match there. Title matches are highlighted, description
	// matches only when they are one run of adjacent characters: a fuzzy
	// match scattered across a sentence highlights noise.
	TitleMatches, DescMatches []int
}

// StyledDelegate renders a channel as two rows: the title with its genre,
// playing and favorite marks and the listener count, then the description.
type StyledDelegate struct {
	list.DefaultDelegate
	// Info reports what the row at index (into the list's items) shows
	// beyond the channel itself; nil shows nothing extra.
	Info func(index int, item Item) RowInfo
	// Frame points at the animation frame the tuning spinner is drawn
	// for; nil draws a still one.
	Frame *int
}

// NewStyledDelegate creates the channel list's delegate.
func NewStyledDelegate(info func(int, Item) RowInfo, frame *int) StyledDelegate {
	d := list.NewDefaultDelegate()
	d.ShowDescription = true // two lines per row, with one line between rows
	return StyledDelegate{DefaultDelegate: d, Info: info, Frame: frame}
}

// rowIndent is the space before a row's text: a margin column, the
// selection bar, and the gap after it.
const rowIndent = 3

// Render renders a list item: selection bar, marks, title (search matches
// highlighted), genre or tuning label, and listener count, then the
// description below.
func (d StyledDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(Item)
	if !ok {
		return
	}
	var info RowInfo
	if d.Info != nil {
		info = d.Info(index, i)
	}
	isSelected := index == m.Index()

	titleStyle, descStyle, countStyle := normalTitleStyle, normalDescStyle, countNormalStyle
	switch {
	case isSelected:
		titleStyle, descStyle, countStyle = selectedTitleStyle, selectedDescStyle, countSelectedStyle
	case info.Playing:
		titleStyle, countStyle = playingTitleStyle, countPlayingStyle
	}

	leftCol, listenerCol := CalculateColumnWidths(m.Width())
	textWidth := leftCol - rowIndent

	// Marks go ahead of the title: the heart, then the playing mark.
	var marks string
	if info.Favorite {
		marks += FavoriteStyle.Render("♥") + " "
	}
	if info.Playing {
		marks += playingMarkStyle.Render("▶") + " "
	}

	// The title gets what the marks leave; the tuning label, or else the
	// genre, whatever the title leaves, if that is enough to be worth
	// showing.
	room := textWidth - lipgloss.Width(marks)
	title := ansi.Truncate(i.Channel.Title, max(room, 0), "…")
	line := marks + highlight(title, info.TitleMatches, titleStyle)
	rest := room - lipgloss.Width(title) - 2
	switch genre := FormatGenre(i.Channel.Genre); {
	case info.Tuning != "" && rest >= 3:
		frame := 0
		if d.Frame != nil {
			frame = *d.Frame
		}
		line += "  " + ansi.Truncate(SpinnerStyle.Render(Spinner(frame))+" "+tuningStyle.Render(info.Tuning), max(rest, 0), "…")
	case genre != "" && rest >= 6:
		line += "  " + genreStyle.Render(ansi.Truncate(genre, rest, "…"))
	}

	count := i.Listeners()
	listeners := strings.Repeat(" ", max(listenerCol-lipgloss.Width(count), 0)) + countStyle.Render(count)

	// The description has the whole line, listener column included.
	desc := ansi.Truncate(lineBreaks.Replace(i.Description()), textWidth+listenerCol, "…")

	bar := " "
	if isSelected {
		bar = selectBarStyle.Render("┃")
	}
	prefix := " " + bar + " "
	_, _ = fmt.Fprintf(w, "%s%s%s\n%s%s",
		prefix, pad(line, textWidth), listeners,
		prefix, highlight(desc, contiguous(info.DescMatches), descStyle))
}

// contiguous returns matches when they are one run of adjacent indexes,
// else nil.
func contiguous(matches []int) []int {
	for i := 1; i < len(matches); i++ {
		if matches[i] != matches[i-1]+1 {
			return nil
		}
	}
	return matches
}

// highlight renders s in style, the runes at the indexes in matches in the
// search match style instead.
func highlight(s string, matches []int, style lipgloss.Style) string {
	if len(matches) == 0 {
		return style.Render(s)
	}
	return lipgloss.StyleRunes(s, matches, matchStyle.Inherit(style), style)
}

// lineBreaks flattens line breaks to spaces, rune for rune, so search
// match indexes into the raw text still line up.
var lineBreaks = strings.NewReplacer("\n", " ", "\r", " ")

const (
	listenerColumnWidth = 11
	minLeftColumnWidth  = 20
	// rightMargin is the blank column kept at the right edge.
	rightMargin = 1
)

// RenderHeader renders the title line above the list: the title, the
// favorites marker when filtered, how many channels the view holds, and the listener column
// heading, aligned to the same columns the delegate renders rows in.
func RenderHeader(width int, favoritesOnly bool, count int) string {
	leftCol, listenerCol := CalculateColumnWidths(width)
	title := TitleStyle.Render("SomaFM Stations")
	if favoritesOnly {
		title += SubtleStyle.Render(" · ") + FavoritesSectionStyle.Render("Favorites")
	}
	// The indent puts the title in the same column as the row titles below.
	left := "   " + title + SubtleStyle.Render(fmt.Sprintf(" · %d", count))
	heading := SubtleStyle.Render("Listeners")
	return pad(ansi.Truncate(left, leftCol, "…"), leftCol) +
		strings.Repeat(" ", max(listenerCol-lipgloss.Width(heading), 0)) + heading
}

// CalculateColumnWidths returns the left and listener column widths for a
// given total width. The left column includes the row indent; the two
// together leave rightMargin blank at the right edge.
func CalculateColumnWidths(totalWidth int) (leftCol, listenerCol int) {
	listenerCol = listenerColumnWidth
	leftCol = max(totalWidth-listenerCol-rightMargin, minLeftColumnWidth)
	return
}
