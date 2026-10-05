package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullBars returns a visualizer with every bar at the top, settled.
func fullBars(width int) *Visualizer {
	v := &Visualizer{}
	levels := make([]byte, VisualizerBars(width))
	for i := range levels {
		levels[i] = 255
	}
	for range 20 {
		v.Update(levels)
	}
	return v
}

func TestVisualizerBars(t *testing.T) {
	assert.Equal(t, 0, VisualizerBars(1))
	assert.Equal(t, 1, VisualizerBars(2))
	assert.Equal(t, 1, VisualizerBars(4))
	assert.Equal(t, 2, VisualizerBars(5))
	assert.Equal(t, 27, VisualizerBars(80))
}

func TestVisualizerUpdate_RisesFastFallsUnderGravity(t *testing.T) {
	v := &Visualizer{}
	v.Update([]byte{255})
	assert.InDelta(t, vizAttack, v.levels[0], 1e-9)
	v.Update([]byte{255})
	high := v.levels[0]
	v.Update([]byte{0})
	assert.InDelta(t, high*vizDecay, v.levels[0], 1e-9, "a fall is gradual")
	v.Update([]byte{255, 255})
	assert.Len(t, v.levels, 2, "a new band count starts over")
}

func TestVisualizerUpdate_TheSilentTailSettlesBarsToZero(t *testing.T) {
	v := fullBars(5)
	for range 25 { // the daemon's tail of silent frames
		v.Update(make([]byte, 2))
	}
	assert.Equal(t, []float64{0, 0}, v.levels)
	assert.Equal(t, "\n", v.Underlay("\n", 5, 40), "no stub left on a tall screen")
}

func TestUnderlay_FillsBlankCellsAndShadesAroundText(t *testing.T) {
	v := fullBars(11) // bars at columns 0-1, 3-4, 6-7, 9-10
	// The cells beside "ab" (3 and 6) take the shade, not a glyph; the
	// rest of their bars still shows.
	assert.Equal(t, "██  ab █ ██", ansi.Strip(v.Underlay("    ab     ", 11, 1)))
}

func TestUnderlay_RunsToTheScreenEdgeAndPastShortLines(t *testing.T) {
	v := fullBars(11)
	assert.Equal(t, "██ ██ ██ ██", ansi.Strip(v.Underlay("", 11, 1)), "an empty line is all bars")
	assert.Equal(t, "ab ██ ██ ██", ansi.Strip(v.Underlay("ab", 11, 1)), "past the end of a short line")
}

func TestUnderlay_SingleSpacesBetweenWordsTakeNoGlyph(t *testing.T) {
	v := fullBars(11)
	assert.Equal(t, "a b c d e f", ansi.Strip(v.Underlay("a b c d e f", 11, 1)))
}

func TestUnderlay_BarsCarryOnBehindText(t *testing.T) {
	withTrueColor(t)
	v := fullBars(11)
	fill := vizFill(0)
	require.NotEmpty(t, fill)
	got := v.Underlay("    ab     ", 11, 1)
	// "a" sits on bar 3-4, "b" on the gap: only "a" gets the shade, and
	// so does the blank before it; the one after "b" is on the gap too.
	assert.Contains(t, got, fill+" "+ansi.ResetStyle+fill+"a\x1b[49mb")
	assert.Equal(t, 1, strings.Count(got, fill+"a"))

	low := &Visualizer{levels: []float64{0.3 / 1}} // a third of a line: under half
	assert.NotContains(t, low.Underlay("ab", 2, 1), fill, "a bar under half a cell leaves text alone")
}

// withTrueColor renders colors for the test, restoring the profile after.
func withTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func TestUnderlay_OnlyTheFieldGetsBars(t *testing.T) {
	v := fullBars(5)
	got := strings.Split(ansi.Strip(v.Underlay("\n\n", 5, 2)), "\n")
	require.Len(t, got, 3)
	assert.Equal(t, "██ ██", got[0])
	assert.Equal(t, "██ ██", got[1])
	assert.Empty(t, got[2], "below the field")
}

func TestUnderlay_PartialTopCell(t *testing.T) {
	v := &Visualizer{}
	for range 30 {
		v.Update([]byte{128}) // about half of two lines: one full, one empty
	}
	got := strings.Split(ansi.Strip(v.Underlay("\n", 2, 2)), "\n")
	assert.Equal(t, "", strings.TrimSpace(got[0]))
	assert.Equal(t, "██", got[1])

	v.levels[0] = 0.75 // a line and a half
	got = strings.Split(ansi.Strip(v.Underlay("\n", 2, 2)), "\n")
	assert.Equal(t, "▄▄", got[0])
}

func TestUnderlay_StyledTextKeepsItsStyle(t *testing.T) {
	v := fullBars(20)
	bold := "\x1b[1m"
	line := bold + "ab" + strings.Repeat(" ", 10) + "cd\x1b[0m"
	got := v.Underlay(line, 20, 1)
	// The bars reset the style; it is restored before "cd".
	assert.Contains(t, got, bold+"ab")
	assert.Contains(t, got, bold+"cd\x1b[0m")
	assert.Equal(t, 20, ansi.StringWidth(got))
	assert.Contains(t, ansi.Strip(got), "ab ")
}

func TestUnderlay_UntouchedRunsAreWrittenAsTheyCame(t *testing.T) {
	v := &Visualizer{levels: []float64{1, 0, 0, 0}}
	// An underlined space between two words that no bar reaches: the line
	// comes back byte for byte, underline and all.
	line := "\x1b[4mGroove Salad\x1b[0m"
	assert.Equal(t, line, v.Underlay(line, 12, 1))
}

func TestUnderlay_CellsWithABackgroundAreNotBlank(t *testing.T) {
	v := fullBars(12)
	for _, sgr := range []string{"\x1b[7m", "\x1b[41m", "\x1b[48;5;12m", "\x1b[38;2;1;2;3;48;2;4;5;6m", "\x1b[48:5:12m"} {
		line := sgr + strings.Repeat(" ", 12) + "\x1b[0m"
		assert.NotContains(t, v.Underlay(line, 12, 1), "█", "%q", sgr)
	}
	// Ending reverse video leaves a background color in place, and
	// resetting the color leaves reverse video.
	for _, sgr := range []string{"\x1b[41m\x1b[27m", "\x1b[7m\x1b[49m"} {
		line := sgr + strings.Repeat(" ", 12) + "\x1b[0m"
		assert.NotContains(t, v.Underlay(line, 12, 1), "█", "%q", sgr)
	}
	// A foreground color whose parameters look like a background's does
	// not count.
	line := "\x1b[38;5;48m" + strings.Repeat(" ", 12) + "\x1b[0m"
	assert.Contains(t, v.Underlay(line, 12, 1), "█")
}

func TestUnderlay_BackgroundEndsARun(t *testing.T) {
	v := fullBars(11)
	line := "      " + "\x1b[7m" + "  " + "\x1b[0m" + "   "
	// Bars stop a cell short of the reversed cells on both sides, and
	// none shows through them.
	assert.Equal(t, "██ ██    ██", ansi.Strip(v.Underlay(line, 11, 1)))
}

func TestUnderlay_NoBarsLeavesTheViewAlone(t *testing.T) {
	v := &Visualizer{}
	view := lipgloss.NewStyle().Bold(true).Render("x") + "     \n  "
	assert.Equal(t, view, v.Underlay(view, 6, 2))
	v.Update(make([]byte, 2))
	assert.Equal(t, view, v.Underlay(view, 6, 2), "all bars at zero")
}

func TestLerpHex(t *testing.T) {
	assert.Equal(t, "#000000", lerpHex("#000000", "#FFFFFF", 0))
	assert.Equal(t, "#808080", lerpHex("#000000", "#FFFFFF", 0.5))
	assert.Equal(t, "#FFFFFF", lerpHex("#000000", "#FFFFFF", 1))
}
