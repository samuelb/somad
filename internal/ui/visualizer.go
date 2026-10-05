package ui

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Visualizer is the spectrum drawn behind the TUI (ADR-0034): a bar per
// band of the levels the daemon sends, rising with the music and falling
// under its own gravity, drawn only into cells the view leaves blank.
type Visualizer struct {
	levels []float64 // bar heights in [0, 1], lowest band first
}

const (
	// Bars are two cells wide with a one-cell gap, like cava's default.
	vizBarWidth = 2
	vizGap      = 1
	// vizAttack is how far a bar moves towards a higher level per frame,
	// and vizDecay how much of its height it keeps per frame while the
	// level is lower: at 25 frames a second it falls to a tenth in about
	// half a second.
	vizAttack = 0.7
	vizDecay  = 0.83
	// vizFloor is where a falling bar drops to zero: within the daemon's
	// tail of silent frames even from full height (0.83^25 < 0.01).
	vizFloor = 0.01
)

// vizEighths are the partial blocks for a bar's top cell, one to seven
// eighths full.
var vizEighths = []rune("▁▂▃▄▅▆▇")

// VisualizerBars is how many bars fit width cells, and so how many bands
// to ask the daemon for.
func VisualizerBars(width int) int {
	return max((width+vizGap)/(vizBarWidth+vizGap), 0)
}

// Update takes the next frame of levels (0–255 per band).
func (v *Visualizer) Update(levels []byte) {
	if len(v.levels) != len(levels) {
		v.levels = make([]float64, len(levels))
	}
	for i, b := range levels {
		target, cur := float64(b)/255, v.levels[i]
		if target > cur {
			cur += (target - cur) * vizAttack
		} else if cur = max(target, cur*vizDecay); cur < vizFloor {
			cur = 0 // settled: no stub left standing once the frames stop
		}
		v.levels[i] = cur
	}
}

// Reset flattens the bars.
func (v *Visualizer) Reset() {
	v.levels = nil
}

// Underlay draws the bars beneath view, which fills a screen width cells
// wide; they rise from the bottom of its first height lines, and the
// full height stands for the loudest level. A bar shows as its glyphs in
// blank cells and carries on behind anything drawn as the cells'
// background, a shade darker so the text stays legible, with a blank
// cell of that shade on either side so words never run into a bar; cells
// that have a background of their own keep it.
func (v *Visualizer) Underlay(view string, width, height int) string {
	if width <= 0 || height <= 0 || len(v.levels) == 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for y := range min(len(lines), height) {
		if row, lit := v.row(y, width, height); lit {
			step := vizStep(height-1-y, height)
			lines[y] = underlayLine(lines[y], row, width, vizStyles[step], vizFill(step))
		}
	}
	return strings.Join(lines, "\n")
}

// barOffset is the column the first of VisualizerBars(width) bars starts
// in, centering them.
func barOffset(width int) int {
	n := VisualizerBars(width)
	return (width - (n*(vizBarWidth+vizGap) - vizGap)) / 2
}

// row returns the glyphs line y of a height-line field shows of the bars,
// a space where there is none, and whether there is any bar at all.
func (v *Visualizer) row(y, width, height int) ([]rune, bool) {
	row := []rune(strings.Repeat(" ", width))
	n := VisualizerBars(width)
	offset := barOffset(width)
	above := (height - 1 - y) * 8 // eighths of a cell below this line
	lit := false
	for b := range n {
		// The band count lags a resize until the daemon has the new one.
		level := v.levels[b*len(v.levels)/n]
		k := int(math.Round(level*float64(height*8))) - above
		if k <= 0 {
			continue
		}
		g := '█'
		if k < 8 {
			g = vizEighths[k-1]
		}
		lit = true
		x := offset + b*(vizBarWidth+vizGap)
		for i := range vizBarWidth {
			row[x+i] = g
		}
	}
	return row, lit
}

// The bars shade from teal at the bottom to gold at the top, the
// palette's two accents, toned down so the bars stay behind the text.
// Each end is an adaptive pair, like every color (ADR-0020).
var (
	vizBottom = [2]string{"#A6D5D7", "#13666A"} // light, dark
	vizTop    = [2]string{"#E6CF9F", "#8C6A30"}
)

// vizFillShade is how far the background behind text is blended from the
// bar's color towards the terminal's, so text on it keeps its contrast.
const vizFillShade = 0.4

// vizShades is how many steps the gradient takes.
const vizShades = 8

// vizColors are the bar colors of the gradient's steps, bottom first, and
// vizStyles their glyph styles.
var (
	vizColors = func() (colors [vizShades]lipgloss.AdaptiveColor) {
		for i := range colors {
			t := float64(i) / (vizShades - 1)
			colors[i] = lipgloss.AdaptiveColor{
				Light: lerpHex(vizBottom[0], vizTop[0], t),
				Dark:  lerpHex(vizBottom[1], vizTop[1], t),
			}
		}
		return colors
	}()
	vizStyles = func() (styles [vizShades]lipgloss.Style) {
		for i, c := range vizColors {
			styles[i] = lipgloss.NewStyle().Foreground(c)
		}
		return styles
	}()
)

// vizStep returns the gradient step of the line that many lines up from
// the bottom of a height-line field.
func vizStep(up, height int) int {
	if height <= 1 {
		return 0
	}
	return up * (vizShades - 1) / (height - 1)
}

// vizFill returns the SGR sequence that sets the background behind text
// on a bar of gradient step, or "" when the terminal shows no color.
func vizFill(step int) string {
	c, toward := vizColors[step].Dark, "#000000"
	if !lipgloss.HasDarkBackground() {
		c, toward = vizColors[step].Light, "#FFFFFF"
	}
	color := lipgloss.ColorProfile().Color(lerpHex(c, toward, vizFillShade))
	if color == nil || color.Sequence(true) == "" {
		return ""
	}
	return "\x1b[" + color.Sequence(true) + "m"
}

// lerpHex blends two #rrggbb colors, t of the way from a to b.
func lerpHex(a, b string, t float64) string {
	ca, cb := hexRGB(a), hexRGB(b)
	var out [3]int
	for i := range out {
		out[i] = int(math.Round(float64(ca[i]) + (float64(cb[i])-float64(ca[i]))*t))
	}
	return fmt.Sprintf("#%02X%02X%02X", out[0], out[1], out[2])
}

func hexRGB(h string) [3]int {
	n, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
	return [3]int{int(n >> 16 & 0xff), int(n >> 8 & 0xff), int(n & 0xff)}
}

// underlayLine draws bg, a line's worth of bar glyphs, into line, a
// rendered line that may carry SGR styling and may end short of width:
// the glyphs into its blank cells, in style, and fill, a background SGR,
// behind the rest of the cells a bar covers at least half of.
func underlayLine(line string, bg []rune, width int, style lipgloss.Style, fill string) string {
	var out strings.Builder
	var active []string // the SGR sequences in effect since the last reset
	backed := false     // whether they set a background
	filling := false    // whether fill is in effect on out
	col := 0
	run := -1             // where the blanks before col start, -1 for none
	var held bytes.Buffer // the run as it came, SGR changes included

	bar := func(x int) bool { return x < len(bg) && bg[x] != ' ' }
	behind := func(x int) bool {
		return fill != "" && !backed && x < len(bg) && (bg[x] == '█' || bg[x] >= '▄' && bg[x] <= '▇')
	}
	unfill := func() {
		if filling {
			out.WriteString("\x1b[49m")
			filling = false
		}
	}

	// flush writes the run of blanks ending at end with the bars' glyphs
	// in it, but for the cell after what precedes it and before what
	// follows it unless the line ends there, which take the shade behind
	// text. The run's SGR changes were held back; the state they leave is
	// restored after it. A run no bar reaches is written as it came.
	flush := func(end int, lineEnds bool) {
		if run < 0 {
			return
		}
		start := run
		run = -1
		margin := func(x int) bool { return x == start && start > 0 || x == end-1 && !lineEnds }
		reached := false
		for x := start; x < end && !reached; x++ {
			reached = bar(x)
		}
		if !reached {
			_, _ = held.WriteTo(&out)
			return
		}
		held.Reset()
		out.WriteString(ansi.ResetStyle)
		for x := start; x < end; {
			switch {
			case margin(x) && behind(x):
				out.WriteString(fill + " " + ansi.ResetStyle)
				x++
			case margin(x) || !bar(x):
				out.WriteByte(' ')
				x++
			default:
				from := x
				for x < end && bar(x) && !margin(x) {
					x++
				}
				out.WriteString(style.Render(string(bg[from:x])))
			}
		}
		out.WriteString(strings.Join(active, ""))
	}

	var state byte
	for len(line) > 0 {
		seq, w, n, next := ansi.DecodeSequence(line, state, nil)
		state, line = next, line[n:]
		switch {
		case isSGR(seq):
			nextActive := applySGR(active, seq)
			nextBacked := setsBackground(nextActive)
			if run >= 0 && nextBacked {
				flush(col, false)
			}
			if run >= 0 {
				held.WriteString(seq)
			} else {
				out.WriteString(seq)
				// It may have reset the background; a background of the
				// text's own wins over the bar's.
				if filling && !nextBacked {
					out.WriteString(fill)
				}
				filling = filling && !nextBacked
			}
			active, backed = nextActive, nextBacked
		case seq == " " && !backed:
			if run < 0 {
				unfill()
				run = col
			}
			held.WriteByte(' ')
			col++
		default:
			flush(col, false)
			if w > 0 {
				if behind(col) && !filling {
					out.WriteString(fill)
					filling = true
				} else if !behind(col) {
					unfill()
				}
			}
			out.WriteString(seq)
			col += w
		}
	}
	unfill()
	if col < width && run < 0 {
		run = col
	}
	flush(max(col, width), true)
	return out.String()
}

// isSGR reports whether seq is a Select Graphic Rendition sequence.
func isSGR(seq string) bool {
	if len(seq) < 3 || !strings.HasPrefix(seq, "\x1b[") || seq[len(seq)-1] != 'm' {
		return false
	}
	return strings.Trim(seq[2:len(seq)-1], "0123456789;:") == ""
}

// applySGR returns the SGR state after seq: active plus seq, or only seq's
// remainder after a reset.
func applySGR(active []string, seq string) []string {
	params := seq[2 : len(seq)-1]
	if params == "" || params == "0" {
		return nil
	}
	if p, ok := strings.CutPrefix(params, "0;"); ok {
		return []string{"\x1b[" + p + "m"}
	}
	return append(active[:len(active):len(active)], seq)
}

// setsBackground reports whether the SGR state gives cells a background:
// a background color, or reverse video, still in effect.
func setsBackground(active []string) bool {
	reverse, color := false, false
	for _, seq := range active {
		params := strings.Split(seq[2:len(seq)-1], ";")
		for i := 0; i < len(params); i++ {
			head, _, colon := strings.Cut(params[i], ":")
			p, _ := strconv.Atoi(head)
			switch {
			case p == 0:
				reverse, color = false, false
			case p == 7:
				reverse = true
			case p == 27:
				reverse = false
			case p == 48 || (p >= 40 && p <= 47) || (p >= 100 && p <= 107):
				color = true
			case p == 49:
				color = false
			}
			// An extended color's own parameters follow it, unless it
			// carries them colon-separated.
			if (p == 38 || p == 48 || p == 58) && !colon && i+1 < len(params) {
				switch params[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				}
			}
		}
	}
	return reverse || color
}
