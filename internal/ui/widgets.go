package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Card renders body in a rounded box width cells wide (borders included),
// with title set into the top edge on the left and info on the right. The
// labels are rendered by the caller; info is dropped when the edge is too
// short for both, and title truncated when too long even alone. Body lines
// longer than the box wrap; an empty body leaves just the two edges.
func Card(width int, border lipgloss.TerminalColor, title, info, body string) string {
	inner := max(width-2, 2)
	edge := lipgloss.NewStyle().Foreground(border)

	var b strings.Builder
	b.WriteString(cardEdge(edge, "╭", "╮", inner, title, info))
	if body != "" {
		content := lipgloss.NewStyle().Width(inner).Padding(0, 1).Render(body)
		side := edge.Render("│")
		for line := range strings.SplitSeq(content, "\n") {
			b.WriteString("\n" + side + line + side)
		}
	}
	b.WriteString("\n" + edge.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return b.String()
}

// CardContentWidth is the room a Card of the given width leaves for a body
// line: the width less the borders and their one-cell padding.
func CardContentWidth(width int) int {
	return max(width-4, 0)
}

// cardEdge renders a horizontal card edge inner cells wide between the
// corners lc and rc, with the labels left and right set into it.
func cardEdge(edge lipgloss.Style, lc, rc string, inner int, left, right string) string {
	// A label is set off by a space at each side, and keeps one dash
	// between it and its corner.
	need := func(s string) int {
		if s == "" {
			return 0
		}
		return lipgloss.Width(s) + 3
	}
	if need(left)+need(right) > inner {
		right = ""
	}
	if need(left)+1 > inner { // and a dash before the far corner
		left = ansi.Truncate(left, max(inner-4, 0), "…")
	}
	label := func(s string) (string, int) {
		if s == "" {
			return "", 0
		}
		s = " " + s + " "
		return s, lipgloss.Width(s)
	}
	left, lw := label(left)
	right, rw := label(right)
	lead, trail := min(lw, 1), min(rw, 1)
	fill := inner - lead - lw - rw - trail
	return edge.Render(lc+strings.Repeat("─", lead)) + left +
		edge.Render(strings.Repeat("─", fill)) + right +
		edge.Render(strings.Repeat("─", trail)+rc)
}

// SpaceBetween lays left and right out on one line width cells wide, right
// flush with the end. left is truncated when both do not fit; right is
// dropped when even a truncated left would not leave it room.
func SpaceBetween(width int, left, right string) string {
	rw := lipgloss.Width(right)
	if rw > 0 && rw+2 > width {
		right, rw = "", 0
	}
	room := width - rw
	if rw > 0 {
		room -= 2 // keep a gap between the two sides
	}
	left = ansi.Truncate(left, max(room, 0), "…")
	gap := max(width-lipgloss.Width(left)-rw, 0)
	return left + strings.Repeat(" ", gap) + right
}

// equalizerFrames are the frames of the level meter ahead of a playing
// track: three bars that each move at most a few levels per frame.
var equalizerFrames = [...]string{"▃▆▂", "▅▂▇", "▂▇▄", "▆▃▅", "▄▅▂", "▇▃▆", "▃▆▄", "▅▄▇"}

// EqualizerRest is the level meter held still, for a track whose snapshot
// is stale.
const EqualizerRest = "▁▁▁"

// Equalizer returns the frame of the level meter ahead of a playing track,
// for animation frame f.
func Equalizer(frame int) string {
	return equalizerFrames[frame%len(equalizerFrames)]
}

// Spinner returns the frame of the busy spinner shown while connecting or
// loading, for animation frame f.
func Spinner(frame int) string {
	frames := spinner.MiniDot.Frames
	return frames[frame%len(frames)]
}

// VolumeGauge renders volume v (0–1) as a labelled slider with a bar
// barWidth cells wide in accent, or without the bar when barWidth is 0:
// "vol ━━━━━━━━━━━━──── 85%". Zero reads "muted".
func VolumeGauge(v float64, barWidth int, accent lipgloss.TerminalColor) string {
	v = min(max(v, 0), 1)
	pct := int(math.Round(v * 100))
	label := fmt.Sprintf("%d%%", pct)
	if pct == 0 {
		label = "muted"
	}
	out := SubtleStyle.Render("vol ")
	if barWidth > 0 {
		filled := int(math.Round(v * float64(barWidth)))
		// Anything audible lights at least one cell.
		if pct > 0 && filled == 0 {
			filled = 1
		}
		out += lipgloss.NewStyle().Foreground(accent).Render(strings.Repeat("━", filled)) +
			DimStyle.Render(strings.Repeat("━", barWidth-filled)) + " "
	}
	return out + MutedStyle.Render(label)
}

// FormatGenre renders SomaFM's pipe-separated genre field for display:
// "ambient|electronic" becomes "ambient · electronic".
func FormatGenre(genre string) string {
	var parts []string
	for g := range strings.SplitSeq(genre, "|") {
		if g = strings.TrimSpace(g); g != "" {
			parts = append(parts, g)
		}
	}
	return strings.Join(parts, " · ")
}

// HelpSeparator separates the entries of the short help line.
const HelpSeparator = " · "

// ShortHelp renders the enabled bindings' help on one line at most width
// cells wide, ending in an ellipsis where the rest do not fit. It stands in
// for help.Model.ShortHelpView, which overflows the width when an item
// does not fit and the ellipsis would not either.
func ShortHelp(bindings []key.Binding, width int) string {
	var enabled []key.Binding
	for _, kb := range bindings {
		if kb.Enabled() {
			enabled = append(enabled, kb)
		}
	}
	ellipsis := " …"
	ellipsisWidth := lipgloss.Width(ellipsis)
	var b strings.Builder
	used := 0
	for i, kb := range enabled {
		item := HelpKeyStyle.Render(kb.Help().Key) + " " + HelpDescStyle.Render(kb.Help().Desc)
		if used > 0 {
			item = HelpSepStyle.Render(HelpSeparator) + item
		}
		w := lipgloss.Width(item)
		// Leave room for the ellipsis unless this is the last item.
		need := w
		if i < len(enabled)-1 {
			need += ellipsisWidth
		}
		if used+need > width {
			if used+ellipsisWidth <= width {
				b.WriteString(HelpSepStyle.Render(ellipsis))
			}
			break
		}
		b.WriteString(item)
		used += w
	}
	return b.String()
}

// pad right-pads s with spaces to width cells; s is assumed to fit.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(width-lipgloss.Width(s), 0))
}
