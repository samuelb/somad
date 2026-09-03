package server

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/protocol"
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

// supportedFormats lists the stream formats this build decodes, most
// preferred first. A variable so tests can pin it regardless of platform.
var supportedFormats = audio.PreferredFormats

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
	if s.closing {
		// Shutdown has already stopped the player with its generation; a
		// play that bumped past it would commit into a dying process.
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, errors.New("server is shutting down")
	}
	ch, ok := s.findChannelLocked(channelID)
	if !ok {
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, fmt.Errorf("unknown channel: %s", channelID)
	}
	if userInitiated && ch.ID == s.channelID &&
		(s.status == protocol.StatusPlaying || s.status == protocol.StatusConnecting) {
		// Already playing (or connecting to) this exact channel: re-running
		// play would tear the stream down and reconnect for no reason.
		// Enter on the current channel, `soma play <current>`, MPRIS Play,
		// and the tray picker all funnel through here, so this is a no-op
		// rather than an error. A reconnect attempt (userInitiated=false)
		// and a channel that is reconnecting or stopped still go through
		// the normal path below.
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, nil
	}
	// Ending the outgoing channel's pending scrobble also fires on a
	// same-channel reconnect, which is fine: the stream did drop, and a
	// resumed title starts a fresh now-playing entry via handleTrackUpdate.
	// A pending sleep timer is deliberately kept: it must outlive channel
	// switches.
	gen := s.abandonSessionLocked(false)
	s.disarmIdleLocked()
	s.status = protocol.StatusConnecting
	s.channelID = ch.ID
	s.channelTitle = ch.Title
	s.channelArtURL = channelArtURL(ch)
	s.trackTitle = ""
	s.streamErr = ""
	save := func() {}
	if userInitiated {
		s.reconnectAttempt = 0
		s.st.LastSelectedChannelID = ch.ID
		save = s.stageSaveLocked()
	}
	s.broadcastStateLocked()
	playlists := ch.Playlists
	title := ch.Title
	s.mu.Unlock()

	save()

	// Try the playable playlists in preference order (AAC before MP3 where
	// this build decodes it), falling back to the next when one fails to
	// connect or decode.
	formats := supportedFormats()
	candidates := channels.SelectPlaylists(playlists, formats, s.quality)
	if len(candidates) == 0 {
		// Reconnecting cannot conjure up a playlist, so never retry this.
		return s.failConnect(gen, fmt.Errorf("no playable stream for %s (supported formats: %s)",
			title, strings.Join(formats, ", ")), false)
	}

	var lastErr error
	for _, cand := range candidates {
		// A stop or newer play may have arrived while an earlier candidate
		// was resolving or connecting. The newer request owns the state, so
		// back out instead of starting stale audio. This check is only an
		// early exit: the player sees the same generation and refuses to
		// commit a stale one itself, which closes the window between here
		// and player.Play (resolveStreamURL blocks on the network).
		s.mu.Lock()
		superseded := gen != s.playGen
		s.mu.Unlock()
		if superseded {
			return s.Snapshot(), audio.ErrSuperseded
		}

		streamURL, err := resolveStreamURL(cand.URL, s.userAgent)
		if err != nil {
			lastErr = fmt.Errorf("failed to get stream URL: %w", err)
			continue
		}

		if err := s.player.Play(streamURL, cand.Format, gen); err != nil {
			if errors.Is(err, audio.ErrSuperseded) {
				// A newer play/stop request won; it owns the state now.
				return s.Snapshot(), err
			}
			lastErr = fmt.Errorf("failed to start playback: %w", err)
			continue
		}

		log.Printf("playing %s (%s)", title, cand.Format)
		s.mu.Lock()
		defer s.mu.Unlock()
		if gen != s.playGen {
			// The player committed this generation, so whoever bumped the
			// server's counter since has also reached the player with a
			// newer one and replaced the session. Do not stop the player
			// here: that would hit the newer, legitimate session.
			return s.snapshotLocked(), audio.ErrSuperseded
		}
		s.status = protocol.StatusPlaying
		s.reconnectAttempt = 0 // connected: a later drop starts a fresh backoff
		s.updateMPRISLocked()
		return s.broadcastStateLocked(), nil
	}
	return s.failConnect(gen, lastErr, true)
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
	// Nothing committed for this generation, but the previous channel's
	// session may still be playing (a switch whose every candidate failed
	// to resolve): stop it, or its audio would continue under a
	// reconnecting or stopped status. A stop with the current generation
	// is a no-op when no session is committed.
	return s.failStreamLocked(gen, err, retry), err
}

// failStreamLocked is the shared tail of a connect failure and an async
// stream error: release the player session identified by gen, record the
// error, and move to reconnecting (or stopped). Returns the broadcast
// snapshot. Caller holds s.mu.
func (s *Server) failStreamLocked(gen uint64, err error, retry bool) protocol.PlaybackState {
	s.player.Stop(gen)
	s.streamErr = err.Error()
	s.trackTitle = ""
	s.scheduleReconnectOrStopLocked(retry)
	return s.broadcastStateLocked()
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
	// A session that is still fading out after a channel switch can fail
	// during the crossfade; its error must not tear down its successor.
	var se *audio.StreamError
	if errors.As(err, &se) && se.Gen != s.playGen {
		return
	}
	// Stop the player so the failed session's goroutine and audio resources
	// are released instead of lingering until the next play. The current
	// generation targets exactly this session.
	s.failStreamLocked(s.playGen, err, true)
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

// PlayCurrent plays the last-played channel (falling back to the top of the
// catalog) unless something is already playing or connecting, in which case
// it is a no-op. Used by MPRIS Play, the tray, and PlayPause.
func (s *Server) PlayCurrent() (protocol.PlaybackState, error) {
	s.mu.Lock()
	if s.status != protocol.StatusStopped {
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, nil
	}
	idx := s.currentIndexLocked()
	s.mu.Unlock()
	return s.playIndex(idx)
}

// PlayPause toggles between stopped and playing. SomaFM is live radio, so
// "pause" tears the stream down and "unpause" reconnects to the live stream
// rather than resuming a position. Used by MPRIS PlayPause and the pause CLI
// command.
func (s *Server) PlayPause() (protocol.PlaybackState, error) {
	s.mu.Lock()
	stopped := s.status == protocol.StatusStopped
	s.mu.Unlock()
	if stopped {
		return s.PlayCurrent()
	}
	return s.Stop(), nil
}

// PlayRelative plays the channel delta positions away from the current (or
// last played) one in catalog order (favorites first), wrapping around. Used
// by MPRIS Next/Previous and the next/prev CLI commands.
func (s *Server) PlayRelative(delta int) (protocol.PlaybackState, error) {
	s.mu.Lock()
	idx := s.currentIndexLocked()
	if idx >= 0 {
		n := len(s.catalog)
		idx = ((idx+delta)%n + n) % n
	}
	s.mu.Unlock()
	return s.playIndex(idx)
}

// currentIndexLocked returns the catalog index of the current (or last
// played) channel, or 0 when it is not in the catalog, or -1 when the
// catalog is empty. Caller holds s.mu.
func (s *Server) currentIndexLocked() int {
	if len(s.catalog) == 0 {
		return -1
	}
	return max(0, slices.IndexFunc(s.catalog, func(ch channels.Channel) bool { return ch.ID == s.channelID }))
}

// playIndex plays the catalog entry at idx, as returned by
// currentIndexLocked; a negative idx means the catalog is empty.
func (s *Server) playIndex(idx int) (protocol.PlaybackState, error) {
	if idx < 0 {
		return s.Snapshot(), errors.New("no channels loaded")
	}
	s.mu.Lock()
	var id string
	if idx < len(s.catalog) {
		id = s.catalog[idx].ID
	}
	s.mu.Unlock()
	if id == "" {
		return s.Snapshot(), errors.New("no channels loaded")
	}
	return s.Play(id)
}

// Stop halts playback immediately and cancels any pending connect,
// reconnect, or sleep-timer stop.
func (s *Server) Stop() protocol.PlaybackState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked()
}

// stopLocked is Stop's implementation, for callers that already hold s.mu
// (namely the StopIn sleep-timer callback, which must check stopGen and act
// on it in the same critical section — see StopIn). Caller holds s.mu.
func (s *Server) stopLocked() protocol.PlaybackState {
	gen := s.abandonSessionLocked(true)
	s.player.Stop(gen)
	s.status = protocol.StatusStopped
	s.trackTitle = ""
	s.streamErr = ""
	s.reconnectAttempt = 0
	s.updateMPRISLocked()
	s.maybeArmIdleLocked()
	return s.broadcastStateLocked()
}

// abandonSessionLocked disowns the current playback session: it bumps the
// play generation (so a play still connecting cannot commit, and the
// returned generation targets exactly the session being left), cancels a
// pending reconnect, and ends the pending scrobble (if the track played
// long enough; see lastfm.go). With cancelSleepTimer it also drops a
// pending sleep-timer stop. Stop, Shutdown, and a channel switch all start
// here; only what happens next differs. Caller holds s.mu.
func (s *Server) abandonSessionLocked(cancelSleepTimer bool) uint64 {
	if cancelSleepTimer {
		s.cancelStopTimerLocked()
	}
	s.playGen++
	s.cancelReconnectLocked()
	s.endLastfmTrackLocked()
	return s.playGen
}

// StopIn arms (or replaces) a sleep timer that stops playback after d. It
// does not stop now — a play already underway, or one started before the
// timer fires, keeps playing until it does; that is the point of a sleep
// timer ("stop in 45 minutes"). The daemon owns the timer, so it survives
// the requesting client disconnecting or exiting.
func (s *Server) StopIn(d time.Duration) protocol.PlaybackState {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelStopTimerLocked()
	gen := s.stopGen
	s.stopAt = time.Now().Add(d)
	s.stopTimer = time.AfterFunc(d, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// The stale check and the stop happen in one critical section: were
		// the lock released between them, a StopIn landing in that gap could
		// arm a new timer (and re-lock to run this same closure's stopLocked
		// call) before this callback got back to it, stopping a session this
		// now-superseded timer no longer owns.
		if s.stopGen != gen {
			return
		}
		s.stopLocked()
	})
	return s.broadcastStateLocked()
}

// CancelPendingStop cancels a pending sleep-timer stop without stopping
// playback now. A no-op (returning the current snapshot) when none is
// pending.
func (s *Server) CancelPendingStop() protocol.PlaybackState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopTimer == nil {
		return s.snapshotLocked()
	}
	s.cancelStopTimerLocked()
	return s.broadcastStateLocked()
}

// cancelStopTimerLocked cancels and clears any pending sleep-timer stop.
// Bumping stopGen makes a timer whose AfterFunc has already fired (and is
// blocked waiting for s.mu) back out instead of stopping a session it no
// longer owns. Caller holds s.mu.
func (s *Server) cancelStopTimerLocked() {
	s.stopGen++
	stopTimer(&s.stopTimer)
	s.stopAt = time.Time{}
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
	save := s.stageSaveLocked()
	if mirrorToMPRIS && s.mpris != nil {
		s.mpris.SetVolume(v)
	}
	snap := s.broadcastStateLocked()
	s.mu.Unlock()

	save()
	return snap
}

// ToggleMute mutes playback (remembering the current volume so it can be
// restored) or, when already at 0, restores the remembered volume — or a
// sensible default when nothing was remembered, e.g. the volume reached 0
// through explicit steps rather than a previous mute.
func (s *Server) ToggleMute() protocol.PlaybackState {
	s.mu.Lock()
	current := s.player.Volume()
	var target float64
	if current > 0 {
		s.st.MuteVolume(current)
		target = 0
	} else {
		target = s.st.UnmuteVolume()
	}
	s.player.SetVolume(target)
	s.st.SetVolume(target) // clears the pre-mute level when target > 0
	save := s.stageSaveLocked()
	if s.mpris != nil {
		s.mpris.SetVolume(target)
	}
	snap := s.broadcastStateLocked()
	s.mu.Unlock()

	save()
	return snap
}

// handleTrackUpdate publishes a now-playing title from the stream's ICY
// metadata.
func (s *Server) handleTrackUpdate(ti audio.TrackInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The previous stream keeps delivering titles while it fades out under
	// the new one; only titles from the current generation are shown.
	if s.status != protocol.StatusPlaying || ti.Gen != s.playGen {
		return
	}
	s.trackTitle = ti.Title
	s.recordHistoryLocked(s.channelID, s.channelTitle, ti.Title)
	s.updateMPRISLocked()
	s.broadcastStateLocked()
	s.notifyTrackLocked()
	// Ends the previous title's pending scrobble (if it played long enough)
	// and starts tracking/now-playing the new one; see lastfm.go.
	s.updateLastfmLocked(ti.Title)
}

// notifyTrackLocked queues a desktop notification for the just-updated
// track, when notifications are enabled and there is a title to show. The
// actual send happens off s.mu and off this hot path: notifyPipeline.queue
// only enqueues the payload (and, if none is already in flight, starts the
// goroutine that sends it).
func (s *Server) notifyTrackLocked() {
	if s.notifyPipe == nil || s.trackTitle == "" {
		return
	}
	artist, title := audio.SplitTitle(s.trackTitle)
	body := s.channelTitle
	if artist != "" {
		body = artist + " · " + s.channelTitle
	}
	s.notifyPipe.queue(title, body)
}

// channelArtURL picks the largest artwork URL a channel offers, for MPRIS
// mpris:artUrl. Falls back to smaller sizes, then "" when none are set.
func channelArtURL(ch channels.Channel) string {
	switch {
	case ch.XLImage != "":
		return ch.XLImage
	case ch.LargeImage != "":
		return ch.LargeImage
	default:
		return ch.Image
	}
}

func (s *Server) cancelReconnectLocked() {
	stopTimer(&s.reconnectTimer)
}

// updateMPRISLocked mirrors the playback state to the desktop integrations
// (MPRIS and the tray). Both are optional and skipped when absent.
func (s *Server) updateMPRISLocked() {
	playing := s.status == protocol.StatusPlaying
	if s.mpris != nil {
		if playing {
			// The raw ICY title is "Artist - Title" where the stream follows
			// that convention; split it once so MPRIS shows the real artist.
			// Genre/ambient stations (and any title with no " - ") have no
			// separate artist, so fall back to the channel name as before.
			artist, title := audio.SplitTitle(s.trackTitle)
			if artist == "" {
				artist = s.channelTitle
			}
			s.mpris.SetPlaying(s.channelTitle, title, artist, s.channelArtURL)
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
