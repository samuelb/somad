package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settle feeds the model the replies of cmd's spectrum subscriptions, and
// of those the replies ask for, until none is left.
func settle(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	for range 10 {
		if cmd == nil {
			return
		}
		msg := msgOrNil[spectrumSubscribedMsg](cmd)
		if msg == nil {
			return
		}
		_, cmd = m.Update(*msg)
	}
	t.Fatal("subscriptions never settled")
}

// newVizModel is newTestModel with the visualizer notice shortened, so
// running a key's commands does not wait it out.
func newVizModel(t *testing.T) *Model {
	t.Helper()
	prev := vizNoticeFor
	vizNoticeFor = time.Millisecond
	t.Cleanup(func() { vizNoticeFor = prev })
	return newTestModel(t)
}

// pressV presses v n times and returns the last press's command.
func pressV(m *Model, n int) tea.Cmd {
	var cmd tea.Cmd
	for range n {
		_, cmd = sendKey(m, 'v')
	}
	return cmd
}

func TestVisualizer_KeyCyclesTheStylesThenOff(t *testing.T) {
	m := newVizModel(t)
	bands := ui.VisualizerBands(80)

	view := ansi.Strip(m.View())
	settle(t, m, pressV(m, 1))
	assert.Equal(t, ui.VisualizerBars, m.Visualizer)
	assert.Equal(t, []int{bands}, backend(m).spectrumBands, "a band per bar")

	m.Update(SpectrumMsg{Levels: []byte{255}})
	m.Update(SpectrumMsg{Levels: []byte{255}}) // a waterfall row takes two
	plain := view
	for _, want := range []ui.VisualizerMode{ui.VisualizerMirror, ui.VisualizerWave, ui.VisualizerMirrorWave, ui.VisualizerWaterfall} {
		assert.Nil(t, msgOrNil[spectrumSubscribedMsg](pressV(m, 1)), "%s: the subscription stays", want)
		assert.Equal(t, want, m.Visualizer)
		m.vizNotice = false // compare the drawing, not the card's notice
		assert.NotEqual(t, plain, ansi.Strip(m.View()), "%s: drawing from the frames so far", want)
	}

	settle(t, m, pressV(m, 1))
	assert.Equal(t, ui.VisualizerOff, m.Visualizer)
	assert.Equal(t, []int{bands, 0}, backend(m).spectrumBands)
	assert.NotContains(t, m.View(), "█", "off, the visualizer is gone")
}

func TestVisualizer_NoticeNamesTheStyleForAMoment(t *testing.T) {
	m := newVizModel(t)
	pressV(m, 1)
	pressV(m, 1)
	assert.Contains(t, ansi.Strip(m.RenderNowPlaying()), "visualizer: mirror")

	m.Update(vizNoticeMsg{gen: m.vizNoticeGen - 1})
	assert.True(t, m.vizNotice, "the first press's timer does not cut the second's short")
	m.Update(vizNoticeMsg{gen: m.vizNoticeGen})
	assert.NotContains(t, ansi.Strip(m.RenderNowPlaying()), "visualizer")

	pressV(m, 4)
	assert.Contains(t, ansi.Strip(m.RenderNowPlaying()), "visualizer: off")
}

func TestVisualizer_OneRequestInFlight(t *testing.T) {
	m := newVizModel(t)
	first := pressV(m, 1)
	// Round to off and on again, and a resize, while the first request is
	// out: nothing more is sent until it is answered.
	for range 6 {
		assert.Nil(t, msgOrNil[spectrumSubscribedMsg](pressV(m, 1)))
	}
	assert.Equal(t, ui.VisualizerBars, m.Visualizer)
	_, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	assert.Nil(t, cmd)

	// The reply catches up with the resize in one more request.
	settle(t, m, first)
	assert.Equal(t, []int{ui.VisualizerBands(80), ui.VisualizerBands(120)}, backend(m).spectrumBands)
}

func TestVisualizer_FailureTurnsItOff(t *testing.T) {
	m := newVizModel(t)
	m.ServerVersion = "0.15.0"
	m.About.Version = "0.17.0"
	backend(m).spectrumErr = errors.New(`unknown method: "spectrum"`)

	settle(t, m, pressV(m, 1))
	assert.Equal(t, ui.VisualizerOff, m.Visualizer)
	assert.Contains(t, m.RequestErr, `visualizer failed: unknown method: "spectrum"`)
	assert.Contains(t, m.RequestErr, "out of date", "an older server is the likely cause")
}

func TestVisualizer_DisconnectLeavesItToTheReconnect(t *testing.T) {
	m := newVizModel(t)
	backend(m).spectrumErr = client.ErrDisconnected
	settle(t, m, pressV(m, 1))
	assert.Equal(t, ui.VisualizerBars, m.Visualizer)
	assert.Empty(t, m.RequestErr)

	fresh := newFakeBackend()
	_, cmd := m.Update(ServerReconnectedMsg{Backend: fresh, ServerVersion: m.About.Version})
	settle(t, m, cmd)
	assert.Equal(t, []int{ui.VisualizerBands(80)}, fresh.spectrumBands, "subscribed on the new connection")
}

func TestVisualizer_ReplyFromAnOldConnectionIsNotTrusted(t *testing.T) {
	m := newVizModel(t)
	old := msgOrNil[spectrumSubscribedMsg](pressV(m, 1))
	require.NotNil(t, old)

	fresh := newFakeBackend()
	_, cmd := m.Update(ServerReconnectedMsg{Backend: fresh, ServerVersion: m.About.Version})
	assert.NotNil(t, cmd, "a fetch of channels and status")
	_, cmd = m.Update(*old)
	settle(t, m, cmd)
	assert.Equal(t, []int{ui.VisualizerBands(80)}, fresh.spectrumBands)
}

func TestVisualizer_FramesDrawBarsBehindTheListOnly(t *testing.T) {
	m := newVizModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: testChannels()}})
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1})

	m.Update(SpectrumMsg{Levels: []byte{255, 255}})
	assert.NotContains(t, m.View(), "█", "frames are ignored while it is off")

	settle(t, m, pressV(m, 1))
	full := make([]byte, ui.VisualizerBands(80))
	for i := range full {
		full[i] = 255
	}
	for range 20 {
		m.Update(SpectrumMsg{Levels: full})
	}
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	card := -1
	for i, l := range lines {
		if strings.Contains(l, "Now Playing") {
			card = i
			break
		}
	}
	require.Positive(t, card)
	assert.Contains(t, lines[0], "██", "bars reach the top")
	assert.Contains(t, lines[card-1], "██", "and rise from just above the cards")
	for _, l := range lines[card:] {
		assert.NotContains(t, l, "█", "the cards and the help stay clear")
	}
	assert.Contains(t, m.View(), "Groove Salad", "text keeps its place")

	m.Update(ServerLostMsg{})
	assert.NotContains(t, m.View(), "█", "a lost server flattens the bars")
}

func TestVisualizer_FailureAfterTurningItOffIsNotReported(t *testing.T) {
	m := newVizModel(t)
	backend(m).spectrumErr = errors.New("boom")
	first := pressV(m, 1)
	pressV(m, 5) // round to off again before the reply
	settle(t, m, first)
	assert.Equal(t, ui.VisualizerOff, m.Visualizer)
	assert.Empty(t, m.RequestErr)
}

// msgOrNil runs cmd, and every command of a batch it returns, and returns
// the first message of type T, or nil when there is none.
func msgOrNil[T tea.Msg](cmd tea.Cmd) *T {
	switch msg := runCmd(cmd).(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			if found := msgOrNil[T](c); found != nil {
				return found
			}
		}
	case T:
		return &msg
	}
	return nil
}

func TestVisualizer_EveryPickedStyleIsReported(t *testing.T) {
	m := newVizModel(t)
	var picked []ui.VisualizerMode
	m.OnVisualizer = func(mode ui.VisualizerMode) { picked = append(picked, mode) }
	pressV(m, 6)
	assert.Equal(t, []ui.VisualizerMode{
		ui.VisualizerBars, ui.VisualizerMirror, ui.VisualizerWave,
		ui.VisualizerMirrorWave, ui.VisualizerWaterfall, ui.VisualizerOff,
	}, picked)
}

func TestVisualizer_ARememberedStyleSubscribesAtStart(t *testing.T) {
	m := newVizModel(t)
	m.Visualizer = ui.VisualizerWave
	msg := msgOrNil[spectrumSubscribedMsg](m.Init())
	require.NotNil(t, msg)
	assert.Equal(t, ui.VisualizerBands(80), msg.bands)
	assert.False(t, m.vizNotice, "no notice for a style nobody just picked")

	m = newVizModel(t)
	assert.Nil(t, msgOrNil[spectrumSubscribedMsg](m.Init()), "off: no subscription")
}
