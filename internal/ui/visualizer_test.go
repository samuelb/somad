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
	levels := make([]byte, VisualizerBands(width))
	for i := range levels {
		levels[i] = 255
	}
	for range 20 {
		v.Update(levels)
	}
	return v
}

func TestVisualizerBands(t *testing.T) {
	assert.Equal(t, 0, VisualizerBands(1))
	assert.Equal(t, 1, VisualizerBands(2))
	assert.Equal(t, 1, VisualizerBands(4))
	assert.Equal(t, 2, VisualizerBands(5))
	assert.Equal(t, 27, VisualizerBands(80))
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
	assert.Equal(t, "\n", v.Underlay("\n", VisualizerBars, 5, 40), "no stub left on a tall screen")
}

func TestUnderlay_FillsBlankCellsAndShadesAroundText(t *testing.T) {
	v := fullBars(11) // bars at columns 0-1, 3-4, 6-7, 9-10
	// The cells beside "ab" (3 and 6) take the shade, not a glyph; the
	// rest of their bars still shows.
	assert.Equal(t, "██  ab █ ██", ansi.Strip(v.Underlay("    ab     ", VisualizerBars, 11, 1)))
}

func TestUnderlay_RunsToTheScreenEdgeAndPastShortLines(t *testing.T) {
	v := fullBars(11)
	assert.Equal(t, "██ ██ ██ ██", ansi.Strip(v.Underlay("", VisualizerBars, 11, 1)), "an empty line is all bars")
	assert.Equal(t, "ab ██ ██ ██", ansi.Strip(v.Underlay("ab", VisualizerBars, 11, 1)), "past the end of a short line")
}

func TestUnderlay_SingleSpacesBetweenWordsTakeNoGlyph(t *testing.T) {
	v := fullBars(11)
	assert.Equal(t, "a b c d e f", ansi.Strip(v.Underlay("a b c d e f", VisualizerBars, 11, 1)))
}

func TestUnderlay_BarsCarryOnBehindText(t *testing.T) {
	withTrueColor(t)
	v := fullBars(11)
	fill := vizFill(0, vizFillShade)
	require.NotEmpty(t, fill)
	got := v.Underlay("    ab     ", VisualizerBars, 11, 1)
	// "a" sits on bar 3-4, "b" on the gap: only "a" gets the shade, and
	// so does the blank before it; the one after "b" is on the gap too.
	assert.Contains(t, got, fill+" "+ansi.ResetStyle+fill+"a\x1b[49mb")
	assert.Equal(t, 1, strings.Count(got, fill+"a"))

	low := &Visualizer{levels: []float64{0.3 / 1}} // a third of a line: under half
	assert.NotContains(t, low.Underlay("ab", VisualizerBars, 2, 1), fill, "a bar under half a cell leaves text alone")
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
	got := strings.Split(ansi.Strip(v.Underlay("\n\n", VisualizerBars, 5, 2)), "\n")
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
	got := strings.Split(ansi.Strip(v.Underlay("\n", VisualizerBars, 2, 2)), "\n")
	assert.Equal(t, "", strings.TrimSpace(got[0]))
	assert.Equal(t, "██", got[1])

	v.levels[0] = 0.75 // a line and a half
	got = strings.Split(ansi.Strip(v.Underlay("\n", VisualizerBars, 2, 2)), "\n")
	assert.Equal(t, "▄▄", got[0])
}

func TestUnderlay_StyledTextKeepsItsStyle(t *testing.T) {
	v := fullBars(20)
	bold := "\x1b[1m"
	line := bold + "ab" + strings.Repeat(" ", 10) + "cd\x1b[0m"
	got := v.Underlay(line, VisualizerBars, 20, 1)
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
	assert.Equal(t, line, v.Underlay(line, VisualizerBars, 12, 1))
}

func TestUnderlay_CellsWithABackgroundAreNotBlank(t *testing.T) {
	v := fullBars(12)
	for _, sgr := range []string{"\x1b[7m", "\x1b[41m", "\x1b[48;5;12m", "\x1b[38;2;1;2;3;48;2;4;5;6m", "\x1b[48:5:12m"} {
		line := sgr + strings.Repeat(" ", 12) + "\x1b[0m"
		assert.NotContains(t, v.Underlay(line, VisualizerBars, 12, 1), "█", "%q", sgr)
	}
	// Ending reverse video leaves a background color in place, and
	// resetting the color leaves reverse video.
	for _, sgr := range []string{"\x1b[41m\x1b[27m", "\x1b[7m\x1b[49m"} {
		line := sgr + strings.Repeat(" ", 12) + "\x1b[0m"
		assert.NotContains(t, v.Underlay(line, VisualizerBars, 12, 1), "█", "%q", sgr)
	}
	// A foreground color whose parameters look like a background's does
	// not count.
	line := "\x1b[38;5;48m" + strings.Repeat(" ", 12) + "\x1b[0m"
	assert.Contains(t, v.Underlay(line, VisualizerBars, 12, 1), "█")
}

func TestUnderlay_BackgroundEndsARun(t *testing.T) {
	v := fullBars(11)
	line := "      " + "\x1b[7m" + "  " + "\x1b[0m" + "   "
	// Bars stop a cell short of the reversed cells on both sides, and
	// none shows through them.
	assert.Equal(t, "██ ██    ██", ansi.Strip(v.Underlay(line, VisualizerBars, 11, 1)))
}

func TestUnderlay_NoBarsLeavesTheViewAlone(t *testing.T) {
	v := &Visualizer{}
	view := lipgloss.NewStyle().Bold(true).Render("x") + "     \n  "
	assert.Equal(t, view, v.Underlay(view, VisualizerBars, 6, 2))
	v.Update(make([]byte, 2))
	assert.Equal(t, view, v.Underlay(view, VisualizerBars, 6, 2), "all bars at zero")
}

func TestLerpHex(t *testing.T) {
	assert.Equal(t, "#000000", lerpHex("#000000", "#FFFFFF", 0))
	assert.Equal(t, "#808080", lerpHex("#000000", "#FFFFFF", 0.5))
	assert.Equal(t, "#FFFFFF", lerpHex("#000000", "#FFFFFF", 1))
}

func TestVisualizerMode_CyclesThroughTheStylesThenOff(t *testing.T) {
	got := make([]string, 0, 7)
	m := VisualizerOff
	for range 7 {
		m = m.Next()
		got = append(got, m.String())
	}
	assert.Equal(t, []string{"bars", "mirror", "wave", "mirror wave", "waterfall", "off", "bars"}, got)
}

// settled returns a visualizer whose bands have settled at levels.
func settled(levels ...byte) *Visualizer {
	v := &Visualizer{}
	for range 40 {
		v.Update(levels)
	}
	return v
}

func TestUnderlay_MirrorGrowsBothWaysFromTheMiddle(t *testing.T) {
	v := settled(128) // half of each half: one line up, one line down
	got := strings.Split(ansi.Strip(v.Underlay("\n\n\n", VisualizerMirror, 2, 4)), "\n")
	assert.Equal(t, []string{"", "██", "██", ""}, []string{strings.TrimSpace(got[0]), got[1], got[2], strings.TrimSpace(got[3])})

	v = settled(191) // half a line more each way
	got = strings.Split(ansi.Strip(v.Underlay("\n\n\n", VisualizerMirror, 2, 4)), "\n")
	assert.Equal(t, "▄▄", got[0])
	assert.Equal(t, "▀▀", got[3])
}

func TestUnderlay_WaveIsAHillOfBrailleWithABrightOutline(t *testing.T) {
	withTrueColor(t)
	v := settled(255, 255)
	got := strings.Split(v.Underlay("\n", VisualizerWave, 4, 2), "\n")
	for _, line := range got {
		for _, r := range ansi.Strip(line) {
			assert.True(t, r >= 0x2800 && r <= 0x28FF, "%q is braille", r)
		}
	}
	assert.Contains(t, got[1], "⣿⣿⣿⣿", "the body is solid dots")
	bright, _, _ := strings.Cut(vizBrightStyles[vizShades-1].Render("x"), "x")
	assert.Contains(t, got[0], bright, "the outline is at full strength")

	assert.Equal(t, "\n", settled(0, 0).Underlay("\n", VisualizerWave, 4, 2), "silence draws nothing")
}

func TestUnderlay_WaveShadesTextFainterThanBars(t *testing.T) {
	withTrueColor(t)
	got := settled(255).Underlay("ab", VisualizerWave, 2, 1)
	assert.Contains(t, got, vizFill(0, vizFaintShade))
	assert.NotContains(t, got, vizFill(0, vizFillShade))
}

func TestUnderlay_WaterfallScrollsUpAndSkipsQuietBands(t *testing.T) {
	v := &Visualizer{}
	v.Update([]byte{255, 0})
	assert.Equal(t, "\n", v.Underlay("\n", VisualizerWaterfall, 2, 2), "a row takes two frames")
	v.Update([]byte{255, 0})
	got := strings.Split(ansi.Strip(v.Underlay("\n", VisualizerWaterfall, 2, 2)), "\n")
	assert.Equal(t, "█", strings.TrimSpace(got[1]), "the loud band, newest at the bottom; the quiet one is left out")
	assert.Empty(t, strings.TrimSpace(got[0]))

	v.Update([]byte{0, 255})
	v.Update([]byte{0, 255})
	got = strings.Split(ansi.Strip(v.Underlay("\n", VisualizerWaterfall, 2, 2)), "\n")
	assert.Equal(t, "█ ", got[0], "the older row moved up")
	assert.Equal(t, " █", got[1])
}

func TestUpdate_WaterfallHistoryClearsOnceSettled(t *testing.T) {
	v := settled(255)
	require.NotEmpty(t, v.history)
	for range 25 { // the daemon's silent tail
		v.Update([]byte{0})
	}
	assert.Empty(t, v.history, "nothing left to scroll the old rows away")
	assert.Equal(t, "\n", v.Underlay("\n", VisualizerWaterfall, 2, 2))
}

func TestInterpolate(t *testing.T) {
	levels := []float64{0, 1}
	assert.InDelta(t, 0, interpolate(levels, 0), 1e-9, "held flat before the first middle")
	assert.InDelta(t, 0.5, interpolate(levels, 0.5), 1e-9)
	assert.InDelta(t, 1, interpolate(levels, 1), 1e-9)
	assert.Zero(t, interpolate(nil, 0.5))
}

func TestUnderlay_MirrorWaveGrowsBothWaysFromTheMiddle(t *testing.T) {
	withTrueColor(t)
	v := settled(128, 128) // half of each half: one line up, one line down
	got := strings.Split(v.Underlay("\n\n\n", VisualizerMirrorWave, 4, 4), "\n")
	plain := make([]string, len(got))
	for i, l := range got {
		plain[i] = strings.TrimSpace(ansi.Strip(l))
	}
	assert.Equal(t, []string{"", "⣿⣿⣿⣿", "⣿⣿⣿⣿", ""}, plain)
	// Both tips are outline, at the outer step of their halves.
	bright, _, _ := strings.Cut(vizBrightStyles[0].Render("x"), "x")
	assert.Contains(t, got[1], bright)
	assert.Contains(t, got[2], bright)

	assert.Equal(t, "\n\n\n", settled(0, 0).Underlay("\n\n\n", VisualizerMirrorWave, 4, 4), "silence draws nothing")
}

func TestParseVisualizerMode(t *testing.T) {
	for m := range visualizerModes {
		got, ok := ParseVisualizerMode(m.String())
		assert.True(t, ok, m.String())
		assert.Equal(t, m, got)
	}
	got, ok := ParseVisualizerMode("plasma")
	assert.False(t, ok)
	assert.Equal(t, VisualizerOff, got)
}
