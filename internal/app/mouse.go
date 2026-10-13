package app

import (
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// updateMouse handles a mouse event. The wheel steps the volume over the
// volume gauge and moves the list cursor anywhere else, like j/k. A click
// on a channel selects it, and one on the channel already selected plays
// it, like enter. A click on the gauge's bar sets the volume there, and so
// does dragging on from it; a click on the rest of the gauge toggles mute.
// A click on an item of the short help bar presses its key.
func (m *Model) updateMouse(msg tea.MouseMsg) tea.Cmd {
	if msg.Action == tea.MouseActionRelease {
		// Whatever button: legacy mouse reporting does not say which.
		m.draggingVolume = false
		return nil
	}
	if m.Loading || m.Err != nil {
		return nil // neither list nor gauge on screen
	}
	at := m.mouseLayout()
	gaugeX := msg.X - at.gaugeX
	onGauge := at.gaugeShown && msg.Y == at.gaugeY && gaugeX >= 0 && gaugeX < at.gaugeWidth

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if onGauge {
			return m.stepVolume(volumeStep)
		}
		m.List.CursorUp()
	case tea.MouseButtonWheelDown:
		if onGauge {
			return m.stepVolume(-volumeStep)
		}
		m.List.CursorDown()
	case tea.MouseButtonLeft:
		v, onBar := ui.VolumeGaugeAt(gaugeX, at.barWidth)
		switch {
		case msg.Action == tea.MouseActionMotion:
			// A drag follows the pointer along the bar from wherever it
			// goes, clamped at the bar's ends.
			if m.draggingVolume {
				return m.setVolume(v)
			}
		case onGauge && onBar:
			m.draggingVolume = true
			return m.setVolume(v)
		case onGauge:
			return m.toggleMuteCmd()
		case msg.Y == at.helpY:
			for _, sp := range at.help {
				if x := msg.X - helpIndent; x >= sp.Start && x < sp.End {
					return m.pressBinding(sp.Binding)
				}
			}
		default:
			if i := m.rowAt(msg.Y - at.listY); i >= 0 {
				return m.clickRow(i)
			}
		}
	}
	return nil
}

// clickRow handles a click on the row of item i: it selects the item, or
// plays it when it is selected already.
func (m *Model) clickRow(i int) tea.Cmd {
	if i != m.List.Index() {
		m.List.Select(i)
		return nil
	}
	return m.playChannel(m.selectedChannelID())
}

// pressBinding acts as if b's first key were pressed in the list, even
// while the search input has the keyboard, where it would be typed instead.
func (m *Model) pressBinding(b key.Binding) tea.Cmd {
	ks := b.Keys()
	if len(ks) == 0 {
		return nil
	}
	msg := keyMsg(ks[0])
	if cmd, handled := m.updateListKey(msg); handled {
		return cmd
	}
	var cmd tea.Cmd
	m.List, cmd = m.List.Update(msg) // the list's own: up, down, ?
	return cmd
}

// keyMsg is the key press that tea.KeyMsg.String renders as k, for the
// keys the short help shows: named keys, and single characters.
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(k)}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// rowAt is the index of the list item whose row is y lines below the top
// of the list on screen, or -1 for none: the gap between two rows, or past
// the last row on the page.
func (m *Model) rowAt(y int) int {
	n := len(m.List.Items())
	stride := ui.RowHeight + ui.RowSpacing
	if n == 0 || y < 0 || y%stride >= ui.RowHeight {
		return -1
	}
	row := y / stride
	p := m.List.Paginator
	if row >= p.ItemsOnPage(n) {
		return -1
	}
	return p.Page*p.PerPage + row
}

// mouseTargets is where View puts what the mouse can act on, in screen
// cells.
type mouseTargets struct {
	// listY is the line the list starts on.
	listY int
	// gaugeX and gaugeY are where the volume gauge starts, gaugeWidth its
	// width and barWidth its bar's; gaugeShown is false when the
	// now-playing line has no room for it.
	gaugeX, gaugeY, gaugeWidth, barWidth int
	gaugeShown                           bool
	// helpY is the line of the short help bar, and help where its items
	// are on it, from helpIndent; nil without the bar (the full help is
	// not clickable).
	helpY int
	help  []ui.HelpSpan
}

// mouseLayout lays the view out as View does and returns where its mouse
// targets are on screen.
func (m *Model) mouseLayout() mouseTargets {
	above, below := m.chrome()
	listY := chromeHeight(above, nil)
	belowY := listY + lipgloss.Height(m.body())
	// Bubble Tea cuts a view taller than the window from the top.
	if cut := belowY + chromeHeight(nil, below) - m.Height; m.Height > 0 && cut > 0 {
		listY -= cut
		belowY -= cut
	}
	np := m.nowPlayingIndex(below)
	cardY := belowY + chromeHeight(nil, below[:np])
	// The help, when layout keeps it, is all that follows the card.
	var help []ui.HelpSpan
	if np < len(below)-1 && !m.List.Help.ShowAll {
		_, help = ui.ShortHelpSpans(m.List.ShortHelp(), m.helpWidth())
	}

	// The gauge ends the now-playing card's first line, flush right; see
	// RenderNowPlaying and ui.SpaceBetween, which drops it without room.
	width := ui.CardContentWidth(m.cardWidth())
	bar := volumeBarWidth(width)
	gw := ui.VolumeGaugeWidth(bar)
	return mouseTargets{
		listY:      listY,
		gaugeX:     cardContentX + width - gw,
		gaugeY:     cardY + 1, // under the top edge
		gaugeWidth: gw,
		barWidth:   bar,
		gaugeShown: gw+2 <= width,
		helpY:      belowY + chromeHeight(nil, below) - 1,
		help:       help,
	}
}
