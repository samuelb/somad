package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

func TestCard_FramesTheBodyWithLabelsInTheTopEdge(t *testing.T) {
	card := Card(30, BorderColor, "Title", "info", "one\ntwo")

	lines := strings.Split(card, "\n")
	assert.Len(t, lines, 4, "top edge, two body lines, bottom edge")
	assert.Equal(t, "╭─ Title ───────────── info ─╮", lines[0])
	assert.Equal(t, "│ one                        │", lines[1])
	assert.Equal(t, "╰────────────────────────────╯", lines[3])
	for _, line := range lines {
		assert.Equal(t, 30, lipgloss.Width(line))
	}
}

func TestCard_WrapsLongBodyLines(t *testing.T) {
	card := Card(20, BorderColor, "", "", "a sentence far too long for one line")

	lines := strings.Split(card, "\n")
	assert.Greater(t, len(lines), 3)
	for _, line := range lines {
		assert.Equal(t, 20, lipgloss.Width(line))
	}
	assert.Contains(t, card, "line", "the tail survives")
}

func TestCard_DropsInfoThenTruncatesTitleWhenNarrow(t *testing.T) {
	top := func(width int, title, info string) string {
		return strings.Split(Card(width, BorderColor, title, info, ""), "\n")[0]
	}

	assert.Equal(t, "╭─ History ──────╮", top(18, "History", "esc to close"), "info goes first")
	assert.Equal(t, "╭─ A very lo… ─╮", top(16, "A very long title", ""))
	assert.Equal(t, 16, lipgloss.Width(top(16, "A very long title", "")))
}

func TestCard_EmptyBodyIsJustTheEdges(t *testing.T) {
	assert.Equal(t, 2, lipgloss.Height(Card(20, BorderColor, "Title", "", "")))
}

func TestCard_InfoAloneThatDoesNotFitIsDropped(t *testing.T) {
	var card string
	assert.NotPanics(t, func() { card = Card(12, BorderColor, "", "a fairly long info label", "body") })
	for _, line := range strings.Split(card, "\n") {
		assert.Equal(t, 12, lipgloss.Width(line))
	}
}

func TestSpaceBetween(t *testing.T) {
	assert.Equal(t, "left     right", SpaceBetween(14, "left", "right"))
	assert.Equal(t, "a long…  right", SpaceBetween(14, "a long left side", "right"), "left truncates first")
	assert.Equal(t, "left", SpaceBetween(4, "left", "right"), "right goes when it cannot fit")
}

func TestSpinner_Cycles(t *testing.T) {
	assert.NotEqual(t, Spinner(0), Spinner(1))
	assert.Equal(t, Spinner(0), Spinner(len([]rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"))))
}

func TestVolumeGauge(t *testing.T) {
	assert.Equal(t, "vol ━━━━━━━━━━ 70%", VolumeGauge(0.7, 10, PrimaryColor))
	assert.Equal(t, "vol 70%", VolumeGauge(0.7, 0, PrimaryColor), "no bar without room")
	assert.Equal(t, "vol ━━━━━━━━━━ muted", VolumeGauge(0, 10, PrimaryColor))
	assert.Equal(t, "vol ━━━━━━━━━━ 100%", VolumeGauge(1.4, 10, PrimaryColor), "clamped")
}

func TestFormatGenre(t *testing.T) {
	assert.Equal(t, "ambient · electronic", FormatGenre("ambient|electronic"))
	assert.Equal(t, "ambient", FormatGenre(" ambient |"))
	assert.Empty(t, FormatGenre(""))
}

func TestShortHelp_TruncatesWithinTheWidth(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "alpha")),
		key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "bravo")),
		key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "charlie")),
		key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delta"), key.WithDisabled()),
	}

	full := ShortHelp(bindings, 80)
	assert.Equal(t, "a alpha · b bravo · c charlie", full, "disabled bindings are left out")

	// help.Model.ShortHelpView overflows at widths like these; this must
	// never.
	for width := range len(full) + 1 {
		got := ShortHelp(bindings, width)
		assert.LessOrEqual(t, lipgloss.Width(got), width, "width %d: %q", width, got)
	}
	assert.Equal(t, "a alpha · b bravo …", ShortHelp(bindings, 22))
}
