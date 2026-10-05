package app

import (
	"errors"
	"fmt"
	"time"

	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/ui"

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
	// SubscribeSpectrum asks for spectrum frames of bands bands for the
	// visualizer, delivered as SpectrumMsg, or stops them with 0.
	SubscribeSpectrum(bands int) error
	// Shutdown stops the server so the reconnect loop respawns a fresh one; the
	// TUI uses it to upgrade an out-of-date server when the user changes,
	// pauses or stops the stream.
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

// SpectrumMsg carries a frame of spectrum levels for the visualizer.
type SpectrumMsg struct {
	Levels []byte
}

// spectrumSubscribedMsg reports how a spectrum subscription request on
// backend went; see syncSpectrum.
type spectrumSubscribedMsg struct {
	backend Backend
	bands   int
	err     error
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
// the entries, or the error if the request failed. ChannelID is the channel
// the fetch was for, so Update can drop a result that arrives after the
// overlay has moved on (closed, or reopened for a different channel) rather
// than clobbering it with a stale answer.
type HistoryMsg struct {
	ChannelID string
	Entries   []protocol.HistoryEntry
	Err       error
}

// sleepTickMsg re-renders the status bar so the sleep-timer countdown moves.
// gen is the tick chain that scheduled it; see syncSleepTick.
type sleepTickMsg struct {
	gen int
}

// animTickMsg advances the spinners and the equalizer. See syncAnim.
type animTickMsg struct{}

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

// stateCmd runs a state-returning request against the backend and delivers
// the snapshot as a ServerStateMsg, or the failure as a RequestErrorMsg
// labelled op. Every playback command is this shape.
func (m *Model) stateCmd(op string, call func(Backend) (protocol.PlaybackState, error)) tea.Cmd {
	b := m.Backend
	return func() tea.Msg {
		st, err := call(b)
		if err != nil {
			return requestErr(op, err)
		}
		return ServerStateMsg{State: st}
	}
}

// fetchStatus asks the server for the current playback snapshot.
func (m *Model) fetchStatus() tea.Cmd {
	return m.stateCmd("status", Backend.Status)
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
	return m.stateCmd("play", func(b Backend) (protocol.PlaybackState, error) { return b.Play(channelID) })
}

// playPauseCmd toggles between stopped and playing on the server.
func (m *Model) playPauseCmd() tea.Cmd {
	return m.stateCmd("playPause", Backend.PlayPause)
}

// restartCmd shuts the current (out-of-date) server down. The event bridge
// notices the dropped connection and reconnects, spawning a replacement on our
// version; the model resumes any pending action once ServerReconnectedMsg
// arrives. Playback is interrupted regardless, which is why the model only
// restarts on a channel change, pause or stop the user asked for.
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
	return m.stateCmd("stop", Backend.Stop)
}

// setVolumeCmd applies a volume on the server, which clamps and persists it.
func (m *Model) setVolumeCmd(v float64) tea.Cmd {
	return m.stateCmd("volume", func(b Backend) (protocol.PlaybackState, error) { return b.SetVolume(v) })
}

// toggleMuteCmd mutes or unmutes on the server, which remembers the
// pre-mute level and restores it.
func (m *Model) toggleMuteCmd() tea.Cmd {
	return m.stateCmd("mute", Backend.ToggleMute)
}

// syncSleepTick keeps one tick chain running while a sleep timer is
// pending, so the status bar countdown moves; call it after every new
// snapshot. A new or changed deadline starts a fresh chain under a new gen,
// and the ticks of the one it replaces are dropped, so chains never pile up.
func (m *Model) syncSleepTick() tea.Cmd {
	if m.Snapshot.StopAt == m.sleepTickStopAt {
		return nil // the running chain (or none) already fits
	}
	m.sleepTickStopAt = m.Snapshot.StopAt
	m.sleepTickGen++
	return m.sleepTick()
}

// sleepTick schedules the current chain's next tick for when the countdown
// label next changes: about once a minute, then every second in the last
// minute. It returns nil, ending the chain, when no timer is pending or the
// label has reached 0s.
func (m *Model) sleepTick() tea.Cmd {
	at, ok := parseStopAt(m.Snapshot.StopAt)
	if !ok {
		return nil
	}
	delay, ok := sleepTickDelay(time.Until(at))
	if !ok {
		return nil
	}
	gen := m.sleepTickGen
	return tea.Tick(delay, func(time.Time) tea.Msg { return sleepTickMsg{gen: gen} })
}

// animInterval is the time between spinner frames, eqInterval the time
// between frames of the equalizer ahead of a playing track. The equalizer
// ticks slower so a playing TUI wakes only four times a second; the two
// never animate at once.
const (
	animInterval = 120 * time.Millisecond
	eqInterval   = 250 * time.Millisecond
)

// spins reports whether a spinner is on screen: loading, the server
// connecting or reconnecting a stream, or the TUI reconnecting to the
// server (which a version-upgrade restart also goes through).
func (m *Model) spins() bool {
	if m.Err != nil {
		return false // the error screen replaces everything
	}
	if m.Loading || m.ServerLost {
		return true
	}
	switch m.Snapshot.Status {
	case protocol.StatusConnecting, protocol.StatusReconnecting:
		return true
	}
	return false
}

// eqPlays reports whether the equalizer ahead of the track title in the
// now-playing card animates: while a track plays and the snapshot is
// current. A spinner is never on screen then.
func (m *Model) eqPlays() bool {
	return m.Err == nil && !m.Loading && !m.ServerLost &&
		m.Snapshot.Status == protocol.StatusPlaying && m.Snapshot.TrackTitle != ""
}

// animates reports whether anything on screen animates. Stopped is still,
// so the tick chain does not run then.
func (m *Model) animates() bool {
	return m.spins() || m.eqPlays()
}

// syncAnim starts the animation tick chain when something on screen
// animates and no chain is running; call it wherever that can start. The
// chain ends by itself at the first tick with nothing left to animate (see
// Update), so an idle TUI never wakes up, and at most one chain runs.
func (m *Model) syncAnim() tea.Cmd {
	if m.animating || !m.animates() {
		return nil
	}
	m.animating = true
	return m.animTick()
}

// frameInterval is the time until the next animation frame: a spinner's
// pace while one is on screen, the equalizer's otherwise.
func (m *Model) frameInterval() time.Duration {
	if m.spins() {
		return animInterval
	}
	return eqInterval
}

// animTick schedules the next animation frame.
func (m *Model) animTick() tea.Cmd {
	return tea.Tick(m.frameInterval(), func(time.Time) tea.Msg { return animTickMsg{} })
}

// syncSpectrum brings the server's spectrum subscription in line with the
// visualizer: a band per bar across the screen while it shows, none
// otherwise. One request is in flight at a time, and its reply syncs
// again, so a quick toggle or a resize storm cannot reach the server out
// of order (it handles a connection's requests concurrently).
func (m *Model) syncSpectrum() tea.Cmd {
	if m.vizSubscribing {
		return nil
	}
	want := 0
	if m.Visualizer {
		// Past the limit, neighboring bars share a band.
		want = min(max(ui.VisualizerBars(m.screenWidth()), 1), protocol.MaxSpectrumBands)
	}
	if want == m.vizBands {
		return nil
	}
	m.vizSubscribing = true
	b := m.Backend
	return func() tea.Msg {
		return spectrumSubscribedMsg{backend: b, bands: want, err: b.SubscribeSpectrum(want)}
	}
}

// applySpectrumSubscribed records the outcome of a syncSpectrum request
// and returns the next one, if the wanted subscription moved on meanwhile.
func (m *Model) applySpectrumSubscribed(msg spectrumSubscribedMsg) tea.Cmd {
	m.vizSubscribing = false
	if msg.backend != m.Backend {
		return m.syncSpectrum() // the connection it went to is gone
	}
	if msg.err != nil {
		if errors.Is(msg.err, client.ErrDisconnected) {
			return nil // the reconnect subscribes afresh
		}
		if !m.Visualizer {
			return m.syncSpectrum() // turned off meanwhile: nothing to report
		}
		m.Visualizer = false
		m.viz.Reset()
		m.RequestErr = fmt.Sprintf("visualizer failed: %v", msg.err)
		if m.skewed() {
			m.RequestErr += " (the server is out of date; it restarts onto this version at the next channel change, pause or stop)"
		}
		return nil
	}
	m.vizBands = msg.bands
	return m.syncSpectrum()
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
		return HistoryMsg{ChannelID: channelID, Entries: entries, Err: err}
	}
}
