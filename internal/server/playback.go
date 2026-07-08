package server

import (
	"errors"
	"fmt"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/protocol"
	"somad/internal/state"
	"somad/pkg/playlist"
)

// reconnectMaxDelay caps the exponential backoff between reconnect
// attempts. Retries never give up — the server is a long-running daemon and
// playback should come back whenever the network does — so past the cap it
// keeps retrying at this steady interval.
const reconnectMaxDelay = time.Minute

// reconnectBaseDelay is a variable so tests can shrink the backoff.
var reconnectBaseDelay = 2 * time.Second

// reconnectDelay returns the backoff delay before the given attempt
// (1-based): it doubles with every attempt (2s, 4s, ...) and is capped at
// reconnectMaxDelay.
func reconnectDelay(attempt int) time.Duration {
	const maxShift = 30 // bounds the shift so huge attempt counts cannot overflow
	shift := attempt - 1
	if shift > maxShift {
		shift = maxShift
	}
	d := reconnectBaseDelay << shift
	if d > reconnectMaxDelay || d <= 0 {
		d = reconnectMaxDelay
	}
	return d
}

// resolveStreamURL resolves a playlist URL to a stream URL. A variable so
// tests can avoid the network.
var resolveStreamURL = playlist.GetStreamURLFromPlaylist

// Play starts playback of the given channel. It blocks until the stream is
// connected and decoding (or has failed), so callers get synchronous
// semantics; progress snapshots are broadcast to all clients along the way.
func (s *Server) Play(channelID string) (protocol.PlaybackState, error) {
	return s.playChannel(channelID, true)
}

// playChannel connects to a channel. userInitiated distinguishes explicit
// play requests (which persist the channel and reset the reconnect budget)
// from automatic reconnect attempts.
func (s *Server) playChannel(channelID string, userInitiated bool) (protocol.PlaybackState, error) {
	s.mu.Lock()
	ch, ok := s.findChannelLocked(channelID)
	if !ok {
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, fmt.Errorf("unknown channel: %s", channelID)
	}
	s.playGen++
	gen := s.playGen
	s.cancelReconnectLocked()
	s.disarmIdleLocked()
	s.status = protocol.StatusConnecting
	s.channelID = ch.ID
	s.channelTitle = ch.Title
	s.trackTitle = ""
	s.streamErr = ""
	var stateToSave *state.State
	var saveSeq uint64
	if userInitiated {
		s.reconnectAttempt = 0
		s.st.LastSelectedChannelID = ch.ID
		stateToSave = s.st.Clone()
		saveSeq = s.nextSaveSeqLocked()
	}
	s.broadcastStateLocked()
	playlists := ch.Playlists
	title := ch.Title
	s.mu.Unlock()

	if stateToSave != nil {
		s.saveState(saveSeq, stateToSave)
	}

	playlistURL := channels.SelectMP3PlaylistURL(playlists)
	if playlistURL == "" {
		// Reconnecting cannot conjure up a playlist, so never retry this.
		return s.failConnect(gen, fmt.Errorf("no MP3 playlist available for %s", title), false)
	}

	streamURL, err := resolveStreamURL(playlistURL, s.userAgent)
	if err != nil {
		return s.failConnect(gen, fmt.Errorf("failed to get stream URL: %w", err), true)
	}

	if err := s.player.Play(streamURL); err != nil {
		if errors.Is(err, audio.ErrSuperseded) {
			// A newer play/stop request won; it owns the state now.
			return s.Snapshot(), err
		}
		return s.failConnect(gen, fmt.Errorf("failed to start playback: %w", err), true)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.playGen {
		return s.snapshotLocked(), audio.ErrSuperseded
	}
	s.status = protocol.StatusPlaying
	s.reconnectAttempt = 0 // connected: a later drop starts a fresh backoff
	s.updateMPRISLocked()
	s.broadcastStateLocked()
	return s.snapshotLocked(), nil
}

// failConnect records a connect failure for the play attempt identified by
// gen, scheduling a reconnect when the error is retryable.
func (s *Server) failConnect(gen uint64, err error, retry bool) (protocol.PlaybackState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.playGen {
		// Superseded while connecting; the newer request owns the state.
		return s.snapshotLocked(), err
	}
	s.streamErr = err.Error()
	s.trackTitle = ""
	s.scheduleReconnectOrStopLocked(retry)
	s.broadcastStateLocked()
	return s.snapshotLocked(), err
}

// handleStreamError reacts to an async error on the running stream: release
// the audio session and schedule a reconnect.
func (s *Server) handleStreamError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Errors surfacing while connecting are (also) returned synchronously by
	// player.Play, and errors after a stop belong to a torn-down session.
	if s.status != protocol.StatusPlaying {
		return
	}
	// Stop the player so the failed session's goroutine and audio resources
	// are released instead of lingering until the next play.
	s.player.Stop()
	s.trackTitle = ""
	s.streamErr = err.Error()
	s.scheduleReconnectOrStopLocked(true)
	s.broadcastStateLocked()
}

// scheduleReconnectOrStopLocked moves to reconnecting with capped
// exponential backoff when the error is retryable, and to stopped otherwise.
// Reconnecting never gives up on its own; only an explicit stop or a new
// play ends it.
func (s *Server) scheduleReconnectOrStopLocked(retry bool) {
	if retry {
		s.reconnectAttempt++
		s.status = protocol.StatusReconnecting
		gen := s.playGen
		channelID := s.channelID
		s.reconnectTimer = time.AfterFunc(reconnectDelay(s.reconnectAttempt), func() {
			s.mu.Lock()
			stale := s.playGen != gen || s.status != protocol.StatusReconnecting || s.channelID != channelID
			s.mu.Unlock()
			if stale {
				return
			}
			_, _ = s.playChannel(channelID, false)
		})
		return
	}
	s.status = protocol.StatusStopped
	s.reconnectAttempt = 0
	s.updateMPRISLocked()
	s.maybeArmIdleLocked()
}

// PlayRelative plays the channel delta positions away from the current (or
// last played) one in catalog order (favorites first), wrapping around. Used
// by MPRIS Next/Previous and the next/prev CLI commands.
func (s *Server) PlayRelative(delta int) (protocol.PlaybackState, error) {
	s.mu.Lock()
	n := len(s.catalog)
	if n == 0 {
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, errors.New("no channels loaded")
	}
	idx := -1
	for i, ch := range s.catalog {
		if ch.ID == s.channelID {
			idx = i
			break
		}
	}
	var id string
	if idx < 0 {
		id = s.catalog[0].ID
	} else {
		id = s.catalog[((idx+delta)%n+n)%n].ID
	}
	s.mu.Unlock()
	return s.Play(id)
}

// Stop halts playback and cancels any pending connect or reconnect.
func (s *Server) Stop() protocol.PlaybackState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.playGen++
	s.cancelReconnectLocked()
	s.player.Stop()
	s.status = protocol.StatusStopped
	s.trackTitle = ""
	s.streamErr = ""
	s.reconnectAttempt = 0
	s.updateMPRISLocked()
	s.maybeArmIdleLocked()
	s.broadcastStateLocked()
	return s.snapshotLocked()
}

// SetVolume clamps and applies the volume, persists it, and broadcasts the
// new state. mirrorToMPRIS is false when the change came from MPRIS itself.
func (s *Server) SetVolume(v float64, mirrorToMPRIS bool) protocol.PlaybackState {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	s.mu.Lock()
	s.player.SetVolume(v)
	s.st.SetVolume(v)
	stateToSave := s.st.Clone()
	saveSeq := s.nextSaveSeqLocked()
	if mirrorToMPRIS && s.mpris != nil {
		s.mpris.SetVolume(v)
	}
	s.broadcastStateLocked()
	snap := s.snapshotLocked()
	s.mu.Unlock()

	s.saveState(saveSeq, stateToSave)
	return snap
}

// handleTrackUpdate publishes a now-playing title from the stream's ICY
// metadata.
func (s *Server) handleTrackUpdate(ti audio.TrackInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != protocol.StatusPlaying {
		return
	}
	s.trackTitle = ti.Title
	s.updateMPRISLocked()
	s.broadcastStateLocked()
}

func (s *Server) cancelReconnectLocked() {
	if s.reconnectTimer != nil {
		s.reconnectTimer.Stop()
		s.reconnectTimer = nil
	}
}

// updateMPRISLocked mirrors the playback state to the desktop integrations
// (MPRIS and the tray). Both are optional and skipped when absent.
func (s *Server) updateMPRISLocked() {
	playing := s.status == protocol.StatusPlaying
	if s.mpris != nil {
		if playing {
			// Use the channel title as artist since SomaFM streams don't have
			// separate artist info.
			s.mpris.SetPlaying(s.channelTitle, s.trackTitle, s.channelTitle)
		} else {
			s.mpris.SetStopped()
		}
	}
	if s.tray != nil {
		if playing {
			s.tray.SetPlaying(s.channelID, s.channelTitle, s.trackTitle)
		} else {
			s.tray.SetStopped()
		}
	}
}
