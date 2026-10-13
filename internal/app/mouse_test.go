package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// screenPos finds text in m's view as the terminal shows it, cut from the
// top when taller than the window, and returns the cell it starts in.
func screenPos(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if cut := len(lines) - m.Height; cut > 0 {
		lines = lines[cut:]
	}
	for y, line := range lines {
		if i := strings.Index(line, text); i >= 0 {
			return ansi.StringWidth(line[:i]), y
		}
	}
	t.Fatalf("%q not on screen", text)
	return 0, 0
}

// sized resizes m to a width × height window, as the real TUI does.
func sized(m *Model, width, height int) *Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(*Model)
}

func click(m *Model, x, y int) tea.Cmd {
	_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return cmd
}

// gaugeBar returns the screen cell of the volume gauge's first bar cell.
func gaugeBar(t *testing.T, m *Model) (x, y int) {
	t.Helper()
	x, y = screenPos(t, m, "vol ━")
	return x + len("vol "), y
}

func TestMouse_ClickSelectsThenPlays(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	x, y := screenPos(t, m, "Drone Zone")

	cmd := click(m, x, y)
	assert.Nil(t, cmd)
	assert.Equal(t, 1, m.List.Index(), "a click selects the channel")
	assert.Empty(t, backend(m).playIDs, "without playing it")

	cmd = click(m, x, y+1) // its description line
	require.NotNil(t, cmd)
	msgOf[ServerStateMsg](t, cmd)
	assert.Equal(t, []string{"dronezone"}, backend(m).playIDs, "a click on the selected channel plays it")
	assert.Equal(t, "dronezone", m.tuningID(), "marked tuning like enter does")
}

func TestMouse_ClickBetweenRowsDoesNothing(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	_, y := screenPos(t, m, "Drone Zone")

	assert.Nil(t, click(m, 5, y+2), "the blank line under the description")
	assert.Equal(t, 0, m.List.Index())
}

func TestMouse_ClickOnLaterPage(t *testing.T) {
	m := sized(newTestModel(t), 80, 17)
	require.Equal(t, 2, m.List.Paginator.PerPage, "the channels span two pages")
	m.List.Select(2) // the second page's only row
	x, y := screenPos(t, m, "Secret Agent")

	msgOf[ServerStateMsg](t, click(m, x, y))
	assert.Equal(t, []string{"secretagent"}, backend(m).playIDs, "the row maps to its item on this page")
}

func TestMouse_ClickVolumeBarSetsVolume(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	x, y := gaugeBar(t, m)
	bar := volumeBarWidth(78 - 4)
	require.Equal(t, 16, bar)

	cmd := click(m, x+3, y) // the fourth of 16 cells
	msg := msgOf[volumeSetMsg](t, cmd)
	assert.InDelta(t, 0.25, msg.volume, 1e-9)
	m.Update(msg)
	assert.InDelta(t, 0.25, m.Snapshot.Volume, 1e-9)
}

func TestMouse_ClickVolumeLabelTogglesMute(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	x, y := screenPos(t, m, "vol ━")

	msgOf[ServerStateMsg](t, click(m, x, y))
	assert.Equal(t, 1, backend(m).mutes)
}

func TestMouse_DragVolumeFollowsPointerUntilRelease(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	x, y := gaugeBar(t, m)

	first := click(m, x, y)
	require.NotNil(t, first)
	// Dragged past the bar's end, and off the gauge's line: clamped to full.
	_, cmd := m.Update(tea.MouseMsg{X: 79, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	assert.Nil(t, cmd, "one change in flight at a time")

	_, cmd = m.Update(runCmd(first))
	assert.InDelta(t, 1, msgOf[volumeSetMsg](t, cmd).volume, 1e-9, "the reply sends where the drag went")

	m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonNone, Action: tea.MouseActionRelease})
	_, cmd = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	assert.Nil(t, cmd, "no drag after the release")
}

func TestMouse_DragFromListLeavesVolume(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	x, y := screenPos(t, m, "Drone Zone")
	click(m, x, y)

	bx, by := gaugeBar(t, m)
	_, cmd := m.Update(tea.MouseMsg{X: bx, Y: by, Button: tea.MouseButtonLeft, Action: tea.MouseActionMotion})
	assert.Nil(t, cmd)
}

func TestMouse_WheelOverGaugeStepsVolume(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	m.Snapshot.Volume = 0.5
	x, y := gaugeBar(t, m)

	_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	assert.InDelta(t, 0.55, msgOf[volumeSetMsg](t, cmd).volume, 1e-9)
	assert.Equal(t, 0, m.List.Index(), "the list stays put")
}

// TestMouse_TargetsFollowTheLayout clicks the list and the gauge in every
// arrangement of the chrome around them.
func TestMouse_TargetsFollowTheLayout(t *testing.T) {
	tests := []struct {
		name   string
		height int
		setup  func(*Model)
	}{
		{"plain", 24, func(*Model) {}},
		{"search bar", 24, func(m *Model) { m.Searching = true }},
		{"about card", 30, func(m *Model) { m.ShowAbout = true }},
		{"about and history cards", 34, func(m *Model) { m.ShowAbout, m.ShowHistory = true, true }},
		{"full help", 40, func(m *Model) { m.List.Help.ShowAll = true }},
		{"window too short for the view", 13, func(m *Model) { m.ShowAbout = true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			tt.setup(m)
			m = sized(m, 80, tt.height)

			x, y := gaugeBar(t, m)
			cmd := click(m, x+15, y) // the last cell
			assert.InDelta(t, 1, msgOf[volumeSetMsg](t, cmd).volume, 1e-9, "the gauge")
			m.Update(tea.MouseMsg{Action: tea.MouseActionRelease})

			// The selected row, the one sure to be on screen however
			// little room the list has.
			x, y = screenPos(t, m, "Groove Salad")
			msgOf[ServerStateMsg](t, click(m, x, y))
			assert.Equal(t, []string{"groovesalad"}, backend(m).playIDs, "the list")
		})
	}
}

func TestMouse_IgnoredWhileLoading(t *testing.T) {
	m := newTestModel(t)
	m.Loading = true

	assert.Nil(t, click(m, 5, 4))
	_, cmd := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, m.List.Index())
}

func TestMouse_HelpBarItemsPressTheirKeys(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)

	x, y := screenPos(t, m, "↓/j down")
	assert.Nil(t, click(m, x+3, y))
	assert.Equal(t, 1, m.List.Index(), "down moves the cursor like j")

	x, y = screenPos(t, m, "enter play")
	msgOf[ServerStateMsg](t, click(m, x, y))
	assert.Equal(t, []string{"dronezone"}, backend(m).playIDs, "play plays the selected channel")

	x, y = screenPos(t, m, "s stop")
	msgOf[ServerStateMsg](t, click(m, x+5, y)) // the item's last cell
	assert.Equal(t, 1, backend(m).stops)
}

func TestMouse_HelpBarSeparatorDoesNothing(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	x, y := screenPos(t, m, " · s stop")

	assert.Nil(t, click(m, x+1, y))
	assert.Zero(t, backend(m).stops)
}

func TestMouse_HelpBarQuit(t *testing.T) {
	m := sized(newTestModel(t), 200, 24) // wide enough for the whole bar
	x, y := screenPos(t, m, "q quit")

	msgOf[tea.QuitMsg](t, click(m, x, y))
	assert.Zero(t, backend(m).stops, "quitting keeps playback going")
}

func TestMouse_HelpBarActsWhileSearchInputIsOpen(t *testing.T) {
	m := sized(newTestModel(t), 80, 24)
	m.Searching = true
	x, y := screenPos(t, m, "p play/pause")

	msgOf[ServerStateMsg](t, click(m, x, y))
	assert.Equal(t, 1, backend(m).playPauses, "the key acts on playback")
	assert.Empty(t, m.SearchQuery, "rather than being typed into the query")
}

func TestMouse_FullHelpIsNotClickable(t *testing.T) {
	m := newTestModel(t)
	m.List.Help.ShowAll = true
	m = sized(m, 80, 40)
	x, y := screenPos(t, m, "stop")

	assert.Nil(t, click(m, x, y))
	assert.Zero(t, backend(m).stops)
}

// TestKeyMsg_RoundTripsEveryHelpKey makes sure a click on any help item,
// short or full, presses the key the binding matches.
func TestKeyMsg_RoundTripsEveryHelpKey(t *testing.T) {
	m := newTestModel(t)
	bindings := m.List.ShortHelp()
	for _, group := range m.List.FullHelp() {
		bindings = append(bindings, group...)
	}
	for _, b := range bindings {
		if len(b.Keys()) == 0 {
			continue
		}
		k := b.Keys()[0]
		assert.Equal(t, k, keyMsg(k).String())
	}
}
