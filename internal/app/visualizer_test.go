package app

import (
	"errors"
	"strings"
	"testing"

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
		msg := msgOf[spectrumSubscribedMsg](t, cmd)
		_, cmd = m.Update(msg)
	}
	t.Fatal("subscriptions never settled")
}

func TestVisualizer_KeyTogglesTheSubscription(t *testing.T) {
	m := newTestModel(t)
	bars := ui.VisualizerBars(80)

	_, cmd := sendKey(m, 'v')
	assert.True(t, m.Visualizer)
	settle(t, m, cmd)
	assert.Equal(t, []int{bars}, backend(m).spectrumBands, "a band per bar")

	m.Update(SpectrumMsg{Levels: []byte{255}})
	_, cmd = sendKey(m, 'v')
	assert.False(t, m.Visualizer)
	settle(t, m, cmd)
	assert.Equal(t, []int{bars, 0}, backend(m).spectrumBands)
	assert.NotContains(t, m.View(), "█", "off, the bars are gone")
}

func TestVisualizer_WideScreensShareBands(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 2000, Height: 40})
	_, cmd := sendKey(m, 'v')
	settle(t, m, cmd)
	assert.Equal(t, []int{protocol.MaxSpectrumBands}, backend(m).spectrumBands)
	assert.True(t, m.Visualizer)
}

func TestVisualizer_OneRequestInFlight(t *testing.T) {
	m := newTestModel(t)
	_, first := sendKey(m, 'v')
	require.NotNil(t, first)
	// Off and on again, and a resize, while the first request is out:
	// nothing more is sent until it is answered.
	_, cmd := sendKey(m, 'v')
	assert.Nil(t, cmd)
	_, cmd = sendKey(m, 'v')
	assert.Nil(t, cmd)
	_, cmd = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	assert.Nil(t, cmd)

	// The reply catches up with the resize in one more request.
	settle(t, m, first)
	assert.Equal(t, []int{ui.VisualizerBars(80), ui.VisualizerBars(120)}, backend(m).spectrumBands)
}

func TestVisualizer_FailureTurnsItOff(t *testing.T) {
	m := newTestModel(t)
	m.ServerVersion = "0.15.0"
	m.About.Version = "0.17.0"
	backend(m).spectrumErr = errors.New(`unknown method: "spectrum"`)

	_, cmd := sendKey(m, 'v')
	settle(t, m, cmd)
	assert.False(t, m.Visualizer)
	assert.Contains(t, m.RequestErr, `visualizer failed: unknown method: "spectrum"`)
	assert.Contains(t, m.RequestErr, "out of date", "an older server is the likely cause")
}

func TestVisualizer_DisconnectLeavesItToTheReconnect(t *testing.T) {
	m := newTestModel(t)
	backend(m).spectrumErr = client.ErrDisconnected
	_, cmd := sendKey(m, 'v')
	settle(t, m, cmd)
	assert.True(t, m.Visualizer)
	assert.Empty(t, m.RequestErr)

	fresh := newFakeBackend()
	_, cmd = m.Update(ServerReconnectedMsg{Backend: fresh, ServerVersion: m.About.Version})
	settle(t, m, cmd)
	assert.Equal(t, []int{ui.VisualizerBars(80)}, fresh.spectrumBands, "subscribed on the new connection")
}

func TestVisualizer_ReplyFromAnOldConnectionIsNotTrusted(t *testing.T) {
	m := newTestModel(t)
	_, first := sendKey(m, 'v')
	old := runCmd(first)

	fresh := newFakeBackend()
	_, cmd := m.Update(ServerReconnectedMsg{Backend: fresh, ServerVersion: m.About.Version})
	assert.NotNil(t, cmd, "a fetch of channels and status")
	_, cmd = m.Update(old)
	settle(t, m, cmd)
	assert.Equal(t, []int{ui.VisualizerBars(80)}, fresh.spectrumBands)
}

func TestVisualizer_FramesDrawBarsBehindTheListOnly(t *testing.T) {
	m := newTestModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(ServerChannelsMsg{Payload: protocol.ChannelsPayload{Channels: testChannels()}})
	m.applySnapshot(protocol.PlaybackState{Status: protocol.StatusPlaying, ChannelID: "groovesalad", ChannelTitle: "Groove Salad", Volume: 1})

	m.Update(SpectrumMsg{Levels: []byte{255, 255}})
	assert.NotContains(t, m.View(), "█", "frames are ignored while it is off")

	_, cmd := sendKey(m, 'v')
	settle(t, m, cmd)
	full := make([]byte, ui.VisualizerBars(80))
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
	m := newTestModel(t)
	backend(m).spectrumErr = errors.New("boom")
	_, first := sendKey(m, 'v')
	sendKey(m, 'v') // off again before the reply
	settle(t, m, first)
	assert.False(t, m.Visualizer)
	assert.Empty(t, m.RequestErr)
}
