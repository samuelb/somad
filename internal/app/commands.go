package app

import (
	"somatui/internal/protocol"

	tea "github.com/charmbracelet/bubbletea"
)

// Backend is the server-side surface the TUI talks to. It is satisfied by
// *client.Client; tests substitute a fake to avoid sockets.
type Backend interface {
	Status() (protocol.PlaybackState, error)
	Channels() (protocol.ChannelsPayload, error)
	Play(channelID string) (protocol.PlaybackState, error)
	Stop() (protocol.PlaybackState, error)
	SetVolume(v float64) (protocol.PlaybackState, error)
	ToggleFavorite(channelID string) ([]string, error)
}

// ServerStateMsg carries a playback snapshot, either pushed by the server or
// returned by a request. Snapshots are authoritative and idempotent.
type ServerStateMsg struct {
	State protocol.PlaybackState
}

// ServerChannelsMsg carries the channel catalog with favorites and the
// last-played channel.
type ServerChannelsMsg struct {
	Payload protocol.ChannelsPayload
}

// ServerLostMsg reports that the server connection dropped; a reconnect is
// underway in the background.
type ServerLostMsg struct{}

// ServerReconnectedMsg delivers the fresh backend after a reconnect.
type ServerReconnectedMsg struct {
	Backend Backend
}

// ServerGoneMsg reports that reconnecting failed for good.
type ServerGoneMsg struct {
	Err error
}

// fetchStatus asks the server for the current playback snapshot.
func (m *Model) fetchStatus() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.Status()
		if err != nil {
			// Connection failures surface separately via ServerLostMsg.
			return nil
		}
		return ServerStateMsg{State: st}
	}
}

// fetchChannels asks the server for the channel catalog.
func (m *Model) fetchChannels() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		payload, err := b.Channels()
		if err != nil {
			return nil
		}
		return ServerChannelsMsg{Payload: payload}
	}
}

// playCmd starts a channel on the server. Progress and failures arrive as
// pushed state events, so the returned snapshot is just the fast path.
func (m *Model) playCmd(channelID string) tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.Play(channelID)
		if err != nil {
			return nil
		}
		return ServerStateMsg{State: st}
	}
}

// stopCmd halts playback on the server.
func (m *Model) stopCmd() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.Stop()
		if err != nil {
			return nil
		}
		return ServerStateMsg{State: st}
	}
}

// setVolumeCmd applies a volume on the server, which clamps and persists it.
func (m *Model) setVolumeCmd(v float64) tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.SetVolume(v)
		if err != nil {
			return nil
		}
		return ServerStateMsg{State: st}
	}
}
