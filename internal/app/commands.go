package app

import (
	"errors"

	"somad/internal/client"
	"somad/internal/protocol"

	tea "github.com/charmbracelet/bubbletea"
)

// Backend is the server-side surface the TUI talks to. It is satisfied by
// *client.Client; tests substitute a fake to avoid sockets.
type Backend interface {
	Status() (protocol.PlaybackState, error)
	Channels() (protocol.ChannelsPayload, error)
	Play(channelID string) (protocol.PlaybackState, error)
	// PlayPause toggles between stopped and playing (live radio has no real
	// pause: unpausing reconnects to the live stream).
	PlayPause() (protocol.PlaybackState, error)
	Stop() (protocol.PlaybackState, error)
	SetVolume(v float64) (protocol.PlaybackState, error)
	// ToggleMute mutes playback, remembering the current volume to restore,
	// or restores it (or a sensible default) when already muted.
	ToggleMute() (protocol.PlaybackState, error)
	ToggleFavorite(channelID string) ([]string, error)
	// History returns recent now-playing titles, newest first, for the
	// history overlay.
	History(channelID string, limit int) ([]protocol.HistoryEntry, error)
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

// RequestErrorMsg reports a request that failed while the connection stayed
// up; the status bar shows it until the server next answers successfully.
// Connection drops surface as ServerLostMsg instead.
type RequestErrorMsg struct {
	Op  string
	Err error
}

// RestartFailedMsg reports that shutting down an out-of-date server failed
// with the connection still up, so no reconnect (and no replay of a pending
// channel change) will follow.
type RestartFailedMsg struct {
	Err error
}

// FavoritesMsg carries the authoritative favorites list returned by a toggle,
// reconciling the optimistic local flip.
type FavoritesMsg struct {
	Favorites []string
}

// HistoryMsg carries the result of a history fetch for the overlay: either
// the entries, or the error if the request failed.
type HistoryMsg struct {
	Entries []protocol.HistoryEntry
	Err     error
}

// opLoadChannels marks catalog fetches so Update can escalate a failure
// during the initial load to the full error screen.
const opLoadChannels = "loading channels"

// requestErr wraps a failed request as a RequestErrorMsg — except for
// connection loss, which the event bridge already surfaces as ServerLostMsg.
func requestErr(op string, err error) tea.Msg {
	if errors.Is(err, client.ErrDisconnected) {
		return nil
	}
	return RequestErrorMsg{Op: op, Err: err}
}

// fetchStatus asks the server for the current playback snapshot.
func (m *Model) fetchStatus() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.Status()
		if err != nil {
			return requestErr("status", err)
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
			return requestErr(opLoadChannels, err)
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
			return requestErr("play", err)
		}
		return ServerStateMsg{State: st}
	}
}

// playPauseCmd toggles between stopped and playing on the server.
func (m *Model) playPauseCmd() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.PlayPause()
		if err != nil {
			return requestErr("playPause", err)
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
		// outcome normally surfaces there — unless the shutdown request failed
		// with the connection still up, which would otherwise strand the
		// restart (and any pending channel change) silently.
		if err := b.Shutdown(); err != nil && !errors.Is(err, client.ErrDisconnected) {
			return RestartFailedMsg{Err: err}
		}
		return nil
	}
}

// quitCmd exits the TUI. When configured, it also shuts down the playback
// server; otherwise it only closes the frontend.
func (m *Model) quitCmd() tea.Cmd {
	b := m.Backend
	shutdown := m.ShutdownOnExit
	onExit := m.OnExit
	return func() tea.Msg {
		if onExit != nil {
			onExit()
		}
		if shutdown {
			_ = b.Shutdown()
		}
		return tea.QuitMsg{}
	}
}

// stopCmd halts playback on the server.
func (m *Model) stopCmd() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.Stop()
		if err != nil {
			return requestErr("stop", err)
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
			return requestErr("volume", err)
		}
		return ServerStateMsg{State: st}
	}
}

// toggleMuteCmd mutes or unmutes on the server, which remembers the
// pre-mute level and restores it.
func (m *Model) toggleMuteCmd() tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := b.ToggleMute()
		if err != nil {
			return requestErr("mute", err)
		}
		return ServerStateMsg{State: st}
	}
}

// historyOverlayLimit is how many entries the history overlay asks for and
// renders.
const historyOverlayLimit = 20

// historyCmd fetches recent now-playing titles for channelID (the playing
// channel) to populate the history overlay.
func (m *Model) historyCmd(channelID string) tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		entries, err := b.History(channelID, historyOverlayLimit)
		return HistoryMsg{Entries: entries, Err: err}
	}
}
