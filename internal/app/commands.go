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
	// Shutdown stops the server so the reconnect loop respawns a fresh one; the
	// TUI uses it to upgrade an out-of-date server when the user changes or
	// stops the stream.
	Shutdown() error
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

// ServerReconnectedMsg delivers the fresh backend after a reconnect, along with
// the version it reports so the model can tell whether the server is now
// up to date.
type ServerReconnectedMsg struct {
	Backend       Backend
	ServerVersion string
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

// restartCmd shuts the current (out-of-date) server down. The event bridge
// notices the dropped connection and reconnects, spawning a replacement on our
// version; the model resumes any pending action once ServerReconnectedMsg
// arrives. Playback is interrupted regardless, which is why the model only
// restarts on a change or stop the user asked for.
func (m *Model) restartCmd() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		// The bridge drives the reconnect off the closed connection, so the
		// outcome (and any error) surfaces there, not here.
		_ = b.Shutdown()
		return nil
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
