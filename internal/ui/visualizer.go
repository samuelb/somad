package ui

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// VisualizerMode is a style of the spectrum drawn behind the TUI
// (ADR-0034), or none; Next cycles through them in order, off last.
type VisualizerMode int

// The visualizer's styles.
const (
	VisualizerOff        VisualizerMode = iota
	VisualizerBars                      // bars rising from the bottom, like cava
	VisualizerMirror                    // bars mirrored about the middle line
	VisualizerWave                      // a smooth braille hill of the spectrum
	VisualizerMirrorWave                // the hill mirrored about the middle line
	VisualizerWaterfall                 // a spectrogram scrolling upwards
	visualizerModes
)

// Next returns the style after m, VisualizerOff after the last.
func (m VisualizerMode) Next() VisualizerMode {
	return (m + 1) % visualizerModes
}

// ParseVisualizerMode returns the style String names, reporting false for
// a name it does not know.
func ParseVisualizerMode(name string) (VisualizerMode, bool) {
	for m := range visualizerModes {
		if m.String() == name {
			return m, true
		}
	}
	return VisualizerOff, false
}

func (m VisualizerMode) String() string {
	switch m {
	case VisualizerBars:
		return "bars"
	case VisualizerMirror:
		return "mirror"
	case VisualizerWave:
		return "wave"
	case VisualizerMirrorWave:
		return "mirror wave"
	case VisualizerWaterfall:
		return "waterfall"
	default:
		return "off"
	}
}

// Visualizer holds what the visualizer draws from the levels the daemon
// sends: per band, a level that rises with the music and falls under its
// own gravity, and for the waterfall, a history of recent frames.
type Visualizer struct {
	levels  []float64   // smoothed levels in [0, 1], lowest band first
	history [][]float64 // waterfall rows, a frame each, oldest first
	paint   *vizPaint   // the SGR sequences it draws with, made for the terminal at the first draw
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
	// The waterfall adds a row every frame and draws two to a line, so at
	// 25 frames a second it moves half a line at a time, twelve lines a
	// second; it keeps vizHistory rows, enough for a tall screen.
	vizHistory = 512
	// vizWaveFloor is the level below which the wave leaves a gap rather
	// than lie along the bottom.
	vizWaveFloor = 0.02
	// vizWaterfallFloor is the level below which the waterfall leaves a
	// cell empty: most of the time a band sits above a third, and coloring
	// all of that would bury the screen. Above it the color fades in out
	// of the terminal's background, so a sound has no hard edge.
	vizWaterfallFloor = 0.35
)

// vizEighths are the partial blocks for a bar's top cell, one to seven
// eighths full.
var vizEighths = []rune("▁▂▃▄▅▆▇")

// VisualizerBars is how many bars fit width cells, and so how many bands
// to ask the daemon for; the other styles interpolate between them.
func VisualizerBands(width int) int {
	return max((width+vizGap)/(vizBarWidth+vizGap), 0)
}

// Update takes the next frame of levels (0–255 per band).
func (v *Visualizer) Update(levels []byte) {
	if len(v.levels) != len(levels) {
		v.levels, v.history = make([]float64, len(levels)), nil
	}
	row := make([]float64, len(levels))
	silent := true
	for i, b := range levels {
		target, cur := float64(b)/255, v.levels[i]
		if target > cur {
			cur += (target - cur) * vizAttack
		} else if cur = max(target, cur*vizDecay); cur < vizFloor {
			cur = 0 // settled: no stub left standing once the frames stop
		}
		v.levels[i], row[i] = cur, target
		silent = silent && cur == 0
	}
	v.history = append(v.history, row)
	if len(v.history) > vizHistory {
		v.history = v.history[len(v.history)-vizHistory:]
	}
	if silent {
		// The last frame of the daemon's silent tail: nothing left that
		// would move the waterfall's older rows off the screen.
		v.history = nil
	}
}

// Reset flattens the visualizer.
func (v *Visualizer) Reset() {
	*v = Visualizer{}
}

// Underlay draws the visualizer in style mode beneath view, which fills a
// screen width cells wide, in its first height lines. It shows as glyphs
// in blank cells and carries on behind anything drawn as the cells'
// background, a shade darker so the text stays legible, with a blank cell
// of that shade on either side so words never run into it; cells that
// have a background of their own keep it.
func (v *Visualizer) Underlay(view string, mode VisualizerMode, width, height int) string {
	if mode == VisualizerOff || width <= 0 || height <= 0 || len(v.levels) == 0 {
		return view
	}
	grid := v.grid(mode, width, height)
	if p := v.paint; p == nil || p.profile != lipgloss.ColorProfile() || p.dark != lipgloss.HasDarkBackground() {
		v.paint = newVizPaint()
	}
	lines := strings.Split(view, "\n")
	for y := range min(len(lines), height) {
		if grid[y] != nil {
			lines[y] = underlayLine(lines[y], grid[y], width, v.paint)
		}
	}
	return strings.Join(lines, "\n")
}

// vizCell is one cell of the visualizer.
type vizCell struct {
	g      rune   // the glyph shown in a blank cell; ' ' for none
	fg, bg vizInk // the glyph's color, and the one behind it
	fill   vizInk // the color shading text on the cell, none if it fills too little of it
	faint  bool   // whether that shade is fainter, for a sparse glyph
}

// barCell is a cell of a bar of gradient step, its glyph filling enough of
// the cell to shade text on it if solid.
func barCell(g rune, step int, solid bool) vizCell {
	c := vizCell{g: g, fg: barInk(step)}
	if solid {
		c.fill = c.fg
	}
	return c
}

// vizGrid is the visualizer's cells, a row per line; a nil row is empty.
type vizGrid [][]vizCell

func (g vizGrid) put(x, y int, c vizCell) {
	if y < 0 || y >= len(g) || x < 0 {
		return
	}
	if g[y] == nil {
		g[y] = make([]vizCell, x+1, max(x+1, 80))
	}
	for len(g[y]) <= x {
		g[y] = append(g[y], vizCell{})
	}
	g[y][x] = c
}

// grid draws the visualizer in style mode into a height-line field width
// cells wide.
func (v *Visualizer) grid(mode VisualizerMode, width, height int) vizGrid {
	g := make(vizGrid, height)
	switch mode {
	case VisualizerBars:
		v.drawBars(g, width, height)
	case VisualizerMirror:
		v.drawMirror(g, width, height)
	case VisualizerWave:
		v.drawWave(g, width, height)
	case VisualizerMirrorWave:
		v.drawMirrorWave(g, width, height)
	case VisualizerWaterfall:
		v.drawWaterfall(g, width, height)
	}
	for _, row := range g {
		for x := range row {
			if row[x].g == 0 {
				row[x].g = ' '
			}
		}
	}
	return g
}

// barOffset is the column the first of VisualizerBands(width) bars starts
// in, centering them.
func barOffset(width int) int {
	n := VisualizerBands(width)
	return (width - (n*(vizBarWidth+vizGap) - vizGap)) / 2
}

// barLevel is the level of bar b of VisualizerBands(width). The band count
// lags a resize until the daemon has the new one.
func (v *Visualizer) barLevel(b, width int) float64 {
	return v.levels[b*len(v.levels)/VisualizerBands(width)]
}

// putBar fills the bar starting at column x on line y.
func putBar(g vizGrid, x, y int, c vizCell) {
	for i := range vizBarWidth {
		g.put(x+i, y, c)
	}
}

// drawBars draws a bar per band rising from the bottom, the full height
// standing for the loudest level; the gradient runs bottom to top.
func (v *Visualizer) drawBars(g vizGrid, width, height int) {
	for b := range VisualizerBands(width) {
		x := barOffset(width) + b*(vizBarWidth+vizGap)
		total := int(math.Round(v.barLevel(b, width) * float64(height*8)))
		for up := 0; up < height && up*8 < total; up++ {
			k := min(total-up*8, 8)
			glyph := '█'
			if k < 8 {
				glyph = vizEighths[k-1]
			}
			putBar(g, x, height-1-up, barCell(glyph, vizStep(up, height), k >= 4))
		}
	}
}

// drawMirror draws the bars twice from the middle line, up and down; the
// gradient runs from the middle outwards.
func (v *Visualizer) drawMirror(g vizGrid, width, height int) {
	upper := height / 2 // lines above the middle; the rest are below it
	lower := height - upper
	for b := range VisualizerBands(width) {
		x := barOffset(width) + b*(vizBarWidth+vizGap)
		level := v.barLevel(b, width)
		total := int(math.Round(level * float64(upper*8)))
		for r := 0; r < upper && r*8 < total; r++ {
			k := min(total-r*8, 8)
			glyph := '█'
			if k < 8 {
				glyph = vizEighths[k-1]
			}
			putBar(g, x, upper-1-r, barCell(glyph, vizStep(r, upper), k >= 4))
		}
		total = int(math.Round(level * float64(lower*8)))
		for r := 0; r < lower && r*8 < total; r++ {
			k := min(total-r*8, 8)
			glyph := '█'
			switch {
			case k < 4:
				glyph = '▔'
			case k < 8:
				glyph = '▀'
			}
			putBar(g, x, upper+r, barCell(glyph, vizStep(r, lower), k >= 4))
		}
	}
}

// brailleDots are the bits of a braille cell's dots, by column and row.
var brailleDots = [2][4]rune{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

// drawWave draws the spectrum as a smooth hill of braille dots rising
// from the bottom; the gradient runs bottom to top.
func (v *Visualizer) drawWave(g vizGrid, width, height int) {
	rows := 4 * height
	v.drawHills(g, width, height, hill{base: rows - 1, dir: -1, span: rows})
}

// drawMirrorWave draws the hill twice from the middle line, up and down;
// the gradient runs from the middle outwards.
func (v *Visualizer) drawMirrorWave(g vizGrid, width, height int) {
	upper := 4 * (height / 2) // dot rows above the middle line
	v.drawHills(g, width, height,
		hill{base: upper - 1, dir: -1, span: upper},
		hill{base: upper, dir: 1, span: 4*height - upper})
}

// hill is where drawHills grows a hill: from dot row base, a dot at a time
// in direction dir (-1 up, 1 down), up to span dots for the loudest level.
type hill struct {
	base, dir, span int
}

// drawHills draws the spectrum as smooth hills of braille dots, two across
// and four down per cell, blended from band to band: dotted fill, and the
// outline along their tips at full strength. The gradient runs from each
// hill's base outwards.
func (v *Visualizer) drawHills(g vizGrid, width, height int, hills ...hill) {
	dots := make([][]rune, height)
	edge := make([][]bool, height) // cells an outline passes through
	steps := make([][]int, height)
	for i := range dots {
		dots[i], edge[i], steps[i] = make([]rune, width), make([]bool, width), make([]int, width)
	}
	for _, h := range hills {
		cells := (h.span + 3) / 4
		prev := -1
		for xp := range 2 * width {
			level := interpolate(v.levels, (float64(xp)+0.5)/float64(2*width))
			n := int(math.Round(level * float64(h.span)))
			if level < vizWaveFloor || n == 0 {
				prev = -1
				continue
			}
			for i := range n {
				y := h.base + h.dir*i
				dots[y/4][xp/2] |= brailleDots[xp%2][y%4]
				steps[y/4][xp/2] = vizStep(abs(y/4-h.base/4), cells)
			}
			// The outline joins the tip to the last column's, down a
			// steep side too.
			tip := h.base + h.dir*(n-1)
			from, to := tip, tip
			if prev >= 0 {
				from, to = min(prev, tip), max(prev, tip)
			}
			for y := from; y <= to; y++ {
				edge[y/4][xp/2] = true
			}
			prev = tip
		}
	}
	for y, row := range dots {
		for x, bits := range row {
			if bits != 0 {
				c := vizCell{g: 0x2800 + bits, fg: barInk(steps[y][x]), fill: barInk(steps[y][x]), faint: true}
				if edge[y][x] {
					c.fg = brightInk(steps[y][x])
				}
				g.put(x, y, c)
			}
		}
	}
}

func abs(n int) int {
	return max(n, -n)
}

// drawWaterfall draws the recent frames as a spectrogram, newest at the
// bottom, so the music scrolls upwards: a frame per half line, colored by
// loudness, with the upper half block in the glyph's color and the lower
// in the one behind it. The gradient runs from quiet to loud.
func (v *Visualizer) drawWaterfall(g vizGrid, width, height int) {
	newest := len(v.history) - 1
	for up := 0; up < height && 2*up <= newest; up++ {
		lower := v.history[newest-2*up]
		var upper []float64 // above the oldest frame: empty
		if newest-2*up > 0 {
			upper = v.history[newest-2*up-1]
		}
		for x := range width {
			at := (float64(x) + 0.5) / float64(width)
			top, bottom := waterfallInkAt(interpolate(upper, at)), waterfallInkAt(interpolate(lower, at))
			// Text takes the louder half's shade: the inks rise with loudness.
			c := vizCell{g: '▀', fg: top, bg: bottom, fill: max(top, bottom)}
			switch top {
			case bottom:
				c.g, c.bg = '█', 0
			case 0:
				c.g, c.fg, c.bg = '▄', bottom, 0
			}
			if c.fill != 0 {
				g.put(x, height-1-up, c)
			}
		}
	}
}

// waterfallInkAt returns the waterfall's ink for level, none below its
// floor.
func waterfallInkAt(level float64) vizInk {
	if level < vizWaterfallFloor {
		return 0
	}
	loudness := (level - vizWaterfallFloor) / (1 - vizWaterfallFloor)
	return waterfallInk(min(int(loudness*vizWaterfallShades), vizWaterfallShades-1))
}

// interpolate returns the level at t in [0, 1] across levels, which stand
// at the middles of equal slices of the range, blending neighbors.
func interpolate(levels []float64, t float64) float64 {
	n := len(levels)
	if n == 0 {
		return 0
	}
	pos := t*float64(n) - 0.5
	i := int(math.Floor(pos))
	f := pos - float64(i)
	a, b := levels[min(max(i, 0), n-1)], levels[min(max(i+1, 0), n-1)]
	return a + (b-a)*f
}

// The bars shade from teal at the bottom to gold at the top, the
// palette's two accents, toned down so the bars stay behind the text.
// Each end is an adaptive pair, like every color (ADR-0020).
var (
	vizBottom = [2]string{"#A6D5D7", "#13666A"} // light, dark
	vizTop    = [2]string{"#E6CF9F", "#8C6A30"}
)

// vizFillShade is how far the background behind text is blended from the
// bar's color towards the terminal's, so text on it keeps its contrast;
// vizFaintShade is the same for the wave, whose dotted fill looks lighter
// than a solid block of its color.
const (
	vizFillShade  = 0.4
	vizFaintShade = 0.7
)

// vizBackdrop stands in for the terminal's background, which the shades
// behind text blend towards.
var vizBackdrop = [2]string{"#FFFFFF", "#000000"} // light, dark

// vizShades is how many steps the bars' gradient takes, a line each at
// most. The waterfall's runs across a line, where coarse steps would show
// as patches, so it takes vizWaterfallShades, rising out of the terminal's
// background (vizWaterfallFade of the way from the teal towards it) to the
// teal halfway, and on to the gold.
const (
	vizShades          = 8
	vizWaterfallShades = 24
	vizWaterfallFade   = 0.85
)

// The wave's thin line takes the accents at full strength.
var (
	vizBrightBottom = [2]string{"#0E686D", "#1A9096"} // PlayingColor
	vizBrightTop    = [2]string{"#8F6400", "#D8A24D"} // PrimaryColor
)

// vizInk is a color the visualizer draws in, an index into vizInks; the
// zero value is none.
type vizInk uint8

// barInk, brightInk, and waterfallInk return the ink of a step of the
// bars' gradient, of its full-strength version, and of the waterfall's.
// Every ink fits a byte (vizInkCount), which keeps a cell small.
func barInk(step int) vizInk       { return vizInk(1 + step) }               //nolint:gosec // see above
func brightInk(step int) vizInk    { return vizInk(1 + vizShades + step) }   //nolint:gosec // see above
func waterfallInk(step int) vizInk { return vizInk(1 + 2*vizShades + step) } //nolint:gosec // see above

// vizInkCount is how many inks there are, none included.
const vizInkCount = 1 + 2*vizShades + vizWaterfallShades

// vizInks are the inks' colors, light and dark.
var vizInks = func() (inks [vizInkCount][2]string) {
	blend := func(a, b [2]string, t float64) [2]string {
		return [2]string{lerpHex(a[0], b[0], t), lerpHex(a[1], b[1], t)}
	}
	for i := range vizShades {
		t := float64(i) / (vizShades - 1)
		inks[barInk(i)] = blend(vizBottom, vizTop, t)
		inks[brightInk(i)] = blend(vizBrightBottom, vizBrightTop, t)
	}
	faded := blend(vizBottom, vizBackdrop, vizWaterfallFade)
	for i := range vizWaterfallShades {
		if t := 2 * float64(i) / (vizWaterfallShades - 1); t < 1 {
			inks[waterfallInk(i)] = blend(faded, vizBottom, t)
		} else {
			inks[waterfallInk(i)] = blend(vizBottom, vizTop, t-1)
		}
	}
	return inks
}()

// vizStep returns the gradient step of the line that many lines up from
// the base of a height-line field.
func vizStep(up, height int) int {
	if height <= 1 {
		return 0
	}
	return up * (vizShades - 1) / (height - 1)
}

// vizPaint holds the SGR sequences that draw each ink in the terminal's
// color profile and background, "" where it shows no color: as a glyph's
// color, as the color behind it, and as the shades behind text.
type vizPaint struct {
	profile     termenv.Profile
	dark        bool
	fg, bg      [vizInkCount]string
	fill, faint [vizInkCount]string // vizFillShade, vizFaintShade
}

func newVizPaint() *vizPaint {
	p := &vizPaint{profile: lipgloss.ColorProfile(), dark: lipgloss.HasDarkBackground()}
	side := 0 // of the light and dark pairs
	if p.dark {
		side = 1
	}
	for ink := vizInk(1); ink < vizInkCount; ink++ {
		c, toward := vizInks[ink][side], vizBackdrop[side]
		p.fg[ink], p.bg[ink] = p.sgr(c, false), p.sgr(c, true)
		p.fill[ink] = p.sgr(lerpHex(c, toward, vizFillShade), true)
		p.faint[ink] = p.sgr(lerpHex(c, toward, vizFaintShade), true)
	}
	return p
}

// sgr returns the SGR sequence that sets hex as the foreground color, or
// the background, or "" when the terminal shows no color.
func (p *vizPaint) sgr(hex string, background bool) string {
	color := p.profile.Color(hex)
	if color == nil {
		return ""
	}
	seq := color.Sequence(background)
	if seq == "" {
		return ""
	}
	return "\x1b[" + seq + "m"
}

// shade returns the SGR sequence that shades text on c, or "" for none.
func (p *vizPaint) shade(c vizCell) string {
	if c.faint {
		return p.faint[c.fill]
	}
	return p.fill[c.fill]
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

// underlayLine draws cells, a line's worth of the visualizer, into line,
// a rendered line that may carry SGR styling and may end short of width:
// glyphs into its blank cells, and behind the rest of the cells it fills
// enough of, its shade, in p's sequences.
func underlayLine(line string, cells []vizCell, width int, p *vizPaint) string {
	var out strings.Builder
	var active []string // the SGR sequences in effect since the last reset
	backed := false     // whether they set a background
	filling := ""       // the shade in effect on out, "" for none
	col := 0
	run := -1             // where the blanks before col start, -1 for none
	var held bytes.Buffer // the run as it came, SGR changes included

	shown := func(x int) bool { return x < len(cells) && cells[x].g != ' ' }
	// behind returns the shade text in cell x takes, "" for none.
	behind := func(x int) string {
		if backed || x >= len(cells) {
			return ""
		}
		return p.shade(cells[x])
	}
	unfill := func() {
		if filling != "" {
			out.WriteString("\x1b[49m")
			filling = ""
		}
	}

	// flush writes the run of blanks ending at end with the glyphs in it,
	// but for the cell after what precedes it and before what follows it
	// unless the line ends there, which take the shade instead. The run's
	// SGR changes were held back; the state they leave is restored after
	// it. A run the visualizer does not reach is written as it came.
	flush := func(end int, lineEnds bool) {
		if run < 0 {
			return
		}
		start := run
		run = -1
		margin := func(x int) bool { return x == start && start > 0 || x == end-1 && !lineEnds }
		reached := false
		for x := start; x < end && !reached; x++ {
			reached = shown(x)
		}
		if !reached {
			_, _ = held.WriteTo(&out)
			return
		}
		held.Reset()
		out.WriteString(ansi.ResetStyle)
		fg, bg := "", "" // the glyph colors in effect on out
		for x := start; x < end; x++ {
			switch shade := behind(x); {
			case margin(x) && shade != "":
				out.WriteString(shade + " " + ansi.ResetStyle)
				fg, bg = "", ""
			case margin(x) || !shown(x):
				if bg != "" {
					out.WriteString("\x1b[49m")
					bg = ""
				}
				out.WriteByte(' ')
			default:
				c := cells[x]
				if s := p.fg[c.fg]; s != fg {
					out.WriteString(s)
					fg = s
				}
				if s := p.bg[c.bg]; s != bg {
					if bg = s; s == "" {
						s = "\x1b[49m"
					}
					out.WriteString(s)
				}
				if c.bg != 0 && bg == "" {
					c.g = '█' // both halves are there, but only one can show
				}
				out.WriteRune(c.g)
			}
		}
		if fg != "" || bg != "" {
			out.WriteString(ansi.ResetStyle)
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
				// A background of the text's own wins over the shade, which
				// reverse video would otherwise show as the text's color;
				// any other change may have reset the background.
				if nextBacked {
					unfill()
				}
				out.WriteString(seq)
				if !nextBacked && filling != "" {
					out.WriteString(filling)
				}
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
				switch shade := behind(col); {
				case shade == "":
					unfill()
				case shade != filling:
					out.WriteString(shade)
					filling = shade
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
