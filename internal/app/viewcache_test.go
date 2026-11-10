package app

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// playingModel returns a model as the TUI has it while music plays: a
// catalog of 45 channels in a window of the given size, a track playing,
// and the visualizer in style mode, in true color as on a real terminal.
func playingModel(tb testing.TB, width, height int, mode ui.VisualizerMode) *Model {
	tb.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	tb.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := newTestModel(tb)
	m.Visualizer = mode
	catalog := make([]channels.Channel, 45)
	for i := range catalog {
		catalog[i] = channels.Channel{
			ID: fmt.Sprintf("c%d", i), Title: fmt.Sprintf("Channel %d", i), Genre: "ambient",
			Description: "A nicely chilled plate of ambient and downtempo beats and grooves", Listeners: "1000",
		}
	}
	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: catalog, Favorites: []string{"c3", "c7"}}})
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m.Update(ServerStateMsg{State: protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "c3", ChannelTitle: "Channel 3", TrackTitle: "Artist - Track", Volume: 0.8,
	}})
	return m
}

// spectrumFrames returns n frames of levels for a window width cells
// wide, each different from the last.
func spectrumFrames(width, n int) []SpectrumMsg {
	frames := make([]SpectrumMsg, n)
	for f := range frames {
		levels := make([]byte, ui.VisualizerBands(width))
		for i := range levels {
			levels[i] = byte(40 + (i*37+f*53)%200)
		}
		frames[f] = SpectrumMsg{Levels: levels}
	}
	return frames
}

// countingDelegate counts the rows the list renders.
type countingDelegate struct {
	list.ItemDelegate
	rows *int
}

func (d countingDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	*d.rows++
	d.ItemDelegate.Render(w, m, index, item)
}

func TestView_ASpectrumFrameRendersNoListRowAgain(t *testing.T) {
	m := playingModel(t, 160, 45, ui.VisualizerBars)
	rows := 0
	m.List.SetDelegate(countingDelegate{ItemDelegate: ui.NewStyledDelegate(m.rowInfo, &m.frame), rows: &rows})
	frames := spectrumFrames(160, 2)

	m.Update(animTickMsg{})
	still := m.View()
	require.Positive(t, rows, "the view after any other message renders the list")

	rows = 0
	m.Update(frames[0])
	first := m.View()
	m.Update(frames[1])
	second := m.View()
	assert.Zero(t, rows, "a frame changes the visualizer and nothing else")
	assert.NotEqual(t, still, first, "which is drawn behind the view kept")
	assert.NotEqual(t, first, second, "frame after frame")

	m.Update(animTickMsg{})
	m.View()
	assert.Positive(t, rows, "the next message of another kind renders the view afresh")
}

// freshView is the view rendered from scratch: what View returned for every
// message before it kept the view between spectrum frames.
func freshView(m *Model) string {
	view, field := m.renderBase()
	if field == 0 {
		return view
	}
	return m.viz.Underlay(view, m.Visualizer, m.Width, field)
}

// TestView_IsTheViewRenderedAfreshWhateverCameBefore feeds the model
// messages of every kind that changes what is on screen, and spectrum
// frames after each. The frames are drawn behind the view kept from the
// message before them; it must be the view that message left, never an
// older one.
func TestView_IsTheViewRenderedAfreshWhateverCameBefore(t *testing.T) {
	m := playingModel(t, 100, 30, ui.VisualizerBars)
	frames := spectrumFrames(100, 4)
	key := func(r rune) func() tea.Msg {
		return func() tea.Msg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	}
	msg := func(msg tea.Msg) func() tea.Msg { return func() tea.Msg { return msg } }
	playing := protocol.PlaybackState{
		Status: protocol.StatusPlaying, ChannelID: "c3", ChannelTitle: "Channel 3", TrackTitle: "Artist - Next", Volume: 0.8,
	}
	quieter, sleepy := playing, playing
	quieter.Volume = 0.3
	// Twenty seconds clear of where the countdown's label changes.
	sleepy.StopAt = time.Now().Add(90*time.Minute + 20*time.Second).Format(time.RFC3339)
	fewer := make([]channels.Channel, 12)
	for i := range fewer {
		fewer[i] = channels.Channel{ID: fmt.Sprintf("c%d", i), Title: fmt.Sprintf("Station %d", i), Listeners: "7"}
	}

	steps := []struct {
		name string
		msg  func() tea.Msg
	}{
		{"cursor down", msg(tea.KeyMsg{Type: tea.KeyDown})},
		{"track change", msg(ServerStateMsg{State: playing})},
		{"resize", msg(tea.WindowSizeMsg{Width: 120, Height: 40})},
		{"history opens", key('h')},
		{"history arrives", msg(HistoryMsg{ChannelID: "c3", Entries: historyEntries(6)})},
		{"animation tick", msg(animTickMsg{})},
		{"about opens", key('a')},
		{"cards close", msg(tea.KeyMsg{Type: tea.KeyEsc})},
		{"search opens", key('/')},
		{"search typed", key('4')},
		{"search kept", msg(tea.KeyMsg{Type: tea.KeyEnter})},
		{"search cleared", key('c')},
		{"favorite toggled", key('f')},
		{"favorites arrive", msg(FavoritesMsg{Favorites: []string{"c7"}})},
		{"favorites only", key('F')},
		{"all channels", key('F')},
		{"volume set", msg(volumeSetMsg{volume: 0.3, state: quieter})},
		{"request failed", msg(RequestErrorMsg{Op: "play", Err: errors.New("no such channel")})},
		{"sleep timer armed", msg(ServerStateMsg{State: sleepy})},
		{"sleep tick", func() tea.Msg { return sleepTickMsg{gen: m.sleepTickGen} }},
		{"visualizer style", key('v')},
		{"style notice ends", func() tea.Msg { return vizNoticeMsg{gen: m.vizNoticeGen} }},
		{"wheel", msg(tea.MouseMsg{X: 10, Y: 8, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})},
		{"click", msg(tea.MouseMsg{X: 10, Y: 8, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})},
		{"server lost", msg(ServerLostMsg{})},
		{"server back", msg(ServerReconnectedMsg{Backend: newFakeBackend()})},
		{"catalog changes", msg(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: fewer}})},
		{"full help", key('?')},
		{"narrower", msg(tea.WindowSizeMsg{Width: 72, Height: 22})},
		{"stopped", msg(ServerStateMsg{State: protocol.PlaybackState{Status: protocol.StatusStopped, Volume: 0.3}})},
		{"visualizer off", func() tea.Msg { m.Visualizer = ui.VisualizerWaterfall; return key('v')() }},
		{"visualizer on again", key('v')},
		{"daemon gone", msg(ServerGoneMsg{Err: errors.New("gave up")})},
	}
	for i, step := range steps {
		// The first frame comes before the message's view was asked for:
		// Bubble Tea asks after every update, but the view kept must not
		// count on it.
		m.Update(step.msg())
		for f := range 3 {
			m.Update(frames[(i+f)%len(frames)])
			require.Equal(t, f > 0, m.viewReuse, "%s, frame %d after it: only a view rendered since the message is kept", step.name, f+1)
			fresh := freshView(m)
			require.Equal(t, fresh, m.View(), "%s, frame %d after it", step.name, f+1)
		}
	}
}

// BenchmarkSpectrumFrame measures what a frame of the visualizer costs the
// TUI, 25 times a second while music plays: the update and the view after
// it, in a 160x45 window.
func BenchmarkSpectrumFrame(b *testing.B) {
	for _, mode := range []ui.VisualizerMode{ui.VisualizerBars, ui.VisualizerWaterfall} {
		b.Run(mode.String(), func(b *testing.B) {
			m := playingModel(b, 160, 45, mode)
			frames := spectrumFrames(160, 8)
			for _, f := range frames {
				m.Update(f)
				_ = m.View()
			}
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				m.Update(frames[i%len(frames)])
				_ = m.View()
				i++
			}
		})
	}
}
