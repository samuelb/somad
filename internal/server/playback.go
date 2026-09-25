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

// resolveStreamURLs resolves a playlist URL to its stream URLs: the same
// stream on each of SomaFM's mirror hosts, https first. A variable so tests
// can avoid the network.
var resolveStreamURLs = playlist.GetStreamURLsFromPlaylist

// maxStreamMirrors caps how many of a playlist's mirrors one play attempt
// tries before falling back to the next format. SomaFM lists three; the cap
// keeps the worst case of connectCandidates bounded whatever a playlist
// holds.
const maxStreamMirrors = 3

// supportedFormats lists the stream formats this build decodes, most
// preferred first. A variable so tests can pin it regardless of platform.
var supportedFormats = audio.PreferredFormats

// Play starts playback of the given channel. It blocks until the stream is
// connected and decoding (or has failed), so callers get synchronous
// semantics; progress snapshots are broadcast to all clients along the way.
func (s *Server) Play(channelID string) (protocol.PlaybackState, error) {
	attempt, snap, err := s.beginPlay(channelID, true, 0)
	return s.runAttempt(attempt, snap, err)
}

// reconnectChannel is the reconnect timer's play. gen is the play
// generation the reconnect was scheduled under; a stop or newer play since
// then has moved it on, which makes this reconnect stale and a no-op.
func (s *Server) reconnectChannel(channelID string, gen uint64) {
	attempt, snap, err := s.beginPlay(channelID, false, gen)
	_, _ = s.runAttempt(attempt, snap, err)
}

// runAttempt finishes a play that beginPlay claimed. A play runs in three
// parts: beginPlay claims the state under the lock, connectCandidates does
// the network work off it, and commitPlay (or failConnect) records the
// outcome under the lock again; the play generation ties the three
// together, so a newer play or stop that lands in between wins. A nil
// attempt means beginPlay declined (no-op or error) and snap and err are
// its answer.
func (s *Server) runAttempt(attempt *playAttempt, snap protocol.PlaybackState, err error) (protocol.PlaybackState, error) {
	if attempt == nil {
		return snap, err
	}
	attempt.save()
	return s.connectCandidates(attempt)
}

// playAttempt is what beginPlay hands to connectCandidates: the generation
// that owns the attempt, the channel to connect to, and the staged state
// write to run once the lock is released.
type playAttempt struct {
	gen  uint64
	ch   channels.Channel
	save func()
}

// beginPlay validates a play request and, unless it is a no-op, moves the
// server to connecting on the new channel under a fresh generation,
// broadcasting that. A nil attempt means nothing changed (shutting down,
// unknown channel, or already playing this channel) and snap is the reply.
func (s *Server) beginPlay(channelID string, userInitiated bool, reconnectGen uint64) (attempt *playAttempt, snap protocol.PlaybackState, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		// Shutdown has already stopped the player with its generation; a
		// play that bumped past it would commit into a dying process.
		return nil, s.snapshotLocked(), errors.New("server is shutting down")
	}
	if !userInitiated && (s.playGen != reconnectGen ||
		s.status != protocol.StatusReconnecting || s.channelID != channelID) {
		// The reconnect timer fired, but a stop or newer play has taken
		// over since it was scheduled. This check has to sit under the same
		// lock acquisition as the state change below: when the timer
		// callback checked on its own and then re-locked here, a Stop
		// landing in that gap was overridden and playback restarted.
		return nil, s.snapshotLocked(), nil
	}
	ch, ok := s.findChannelLocked(channelID)
	if !ok {
		err := fmt.Errorf("unknown channel: %s", channelID)
		if !userInitiated {
			// A catalog refresh dropped the channel while it was
			// reconnecting: there is nothing left to retry against, so stop
			// with the error shown instead of reconnecting forever (which
			// also kept the idle timeout from ever firing).
			gen := s.abandonSessionLocked(false)
			return nil, s.failStreamLocked(gen, err, false), err
		}
		return nil, s.snapshotLocked(), err
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
		return nil, s.snapshotLocked(), nil
	}
	// Ending the outgoing channel's pending scrobble also fires on a
	// same-channel reconnect, which is fine: the stream did drop, and the
	// resumed title picks the same play back up via handleTrackUpdate
	// (lastfm.go), so it is not scrobbled twice.
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
	return &playAttempt{gen: gen, ch: ch, save: save}, s.broadcastStateLocked(), nil
}

// connectCandidates tries the channel's playable playlists in preference
// order (AAC before MP3 where this build decodes it), and each playlist's
// mirrors in turn: a stream that fails to connect or decode falls back to
// the same format on the next mirror, then to the next format. The first
// mirror tried rotates with the reconnect attempt, so a reconnect after a
// drop starts on a different host than the one that dropped. It runs off
// the lock because resolving and connecting block on the network.
//
// Worst case before the attempt gives up: per candidate, the 15 s playlist
// fetch plus maxStreamMirrors connects of at most 10 s each (the player's
// connect deadline, which runs until audio decodes), so 45 s per format,
// plus one wait of at most 15 s for the audio device, which ends the
// attempt at once rather than being repeated for every stream. The
// client's play-call timeout (internal/client) must stay above this.
func (s *Server) connectCandidates(a *playAttempt) (protocol.PlaybackState, error) {
	formats := supportedFormats()
	candidates := channels.SelectPlaylists(a.ch.Playlists, formats, s.quality)
	if len(candidates) == 0 {
		// Reconnecting cannot conjure up a playlist, so never retry this.
		return s.failConnect(a.gen, fmt.Errorf("no playable stream for %s (supported formats: %s)",
			a.ch.Title, strings.Join(formats, ", ")), false)
	}
	s.mu.Lock()
	rotation := s.reconnectAttempt
	s.mu.Unlock()

	var lastErr error
	for _, cand := range candidates {
		// A stop or newer play may have arrived while an earlier candidate
		// was resolving or connecting. The newer request owns the state, so
		// back out instead of starting stale audio. This check is only an
		// early exit: the player sees the same generation and refuses to
		// commit a stale one itself, which closes the window between here
		// and player.Play (resolveStreamURLs blocks on the network).
		if s.superseded(a.gen) {
			return s.Snapshot(), audio.ErrSuperseded
		}

		mirrors, err := resolveStreamURLs(cand.URL, s.userAgent)
		if err != nil {
			lastErr = fmt.Errorf("failed to get stream URL: %w", err)
			continue
		}

		for _, streamURL := range rotateMirrors(mirrors, rotation) {
			if s.superseded(a.gen) {
				return s.Snapshot(), audio.ErrSuperseded
			}
			err := s.player.Play(streamURL, cand.Format, a.gen)
			if err == nil {
				log.Printf("playing %s (%s from %s)", a.ch.Title, cand.Format, streamURL)
				return s.commitPlay(a.gen)
			}
			if errors.Is(err, audio.ErrSuperseded) {
				// A newer play/stop request won; it owns the state now.
				return s.Snapshot(), err
			}
			lastErr = fmt.Errorf("failed to start playback: %w", err)
			if errors.Is(err, audio.ErrAudioDevice) {
				// Every other stream would wait for the device and fail
				// the same way.
				return s.failConnect(a.gen, lastErr, true)
			}
			log.Printf("stream %s failed: %v", streamURL, err)
		}
	}
	return s.failConnect(a.gen, lastErr, true)
}

// rotateMirrors returns the first maxStreamMirrors mirrors, starting at
// index rotation (modulo their count) and wrapping around.
func rotateMirrors(mirrors []string, rotation int) []string {
	mirrors = mirrors[:min(len(mirrors), maxStreamMirrors)]
	if len(mirrors) == 0 {
		return nil
	}
	start := rotation % len(mirrors)
	return append(slices.Clone(mirrors[start:]), mirrors[:start]...)
}

// superseded reports whether a newer play or stop has taken over since gen.
func (s *Server) superseded(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gen != s.playGen
}

// commitPlay records that the player is streaming the attempt identified by
// gen, unless a newer play or stop has taken over meanwhile.
func (s *Server) commitPlay(gen uint64) (protocol.PlaybackState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.playGen {
		// The player committed this generation, so whoever bumped the
		// server's counter since has also reached the player with a newer
		// one and replaced the session. Do not stop the player here: that
		// would hit the newer, legitimate session.
		return s.snapshotLocked(), audio.ErrSuperseded
	}
	if err := s.connectErr; err != nil {
		// The committed session already failed while this commit waited
		// for the lock (handleStreamError left the error here): going to
		// playing would show a dead stream as healthy and never reconnect.
		return s.failStreamLocked(gen, err, true), err
	}
	s.status = protocol.StatusPlaying
	s.reconnectAttempt = 0 // connected: a later drop starts a fresh backoff
	if title := s.connectTitle; title != "" {
		// Its first title arrived in the same window; publishing it also
		// mirrors the playing state to MPRIS and broadcasts it.
		s.connectTitle = ""
		return s.publishTrackLocked(title), nil
	}
	s.updateMPRISLocked()
	return s.broadcastStateLocked(), nil
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
	// A session that is still fading out after a channel switch can fail
	// during the crossfade; its error must not tear down its successor.
	var se *audio.StreamError
	if errors.As(err, &se) && se.Gen != s.playGen {
		return
	}
	switch s.status {
	case protocol.StatusPlaying:
		// Stop the player so the failed session's goroutine and audio
		// resources are released instead of lingering until the next play.
		// The current generation targets exactly this session.
		s.failStreamLocked(s.playGen, err, true)
	case protocol.StatusConnecting:
		// A failure to connect is only ever returned by player.Play, never
		// reported here, so this is the session the player just committed
		// failing before commitPlay got the lock. commitPlay acts on it.
		s.connectErr = err
	}
	// Otherwise (stopped, reconnecting) it belongs to a torn-down session.
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
			s.reconnectChannel(channelID, gen)
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
	id := s.relativeChannelIDLocked(0)
	s.mu.Unlock()
	return s.playChannelID(id)
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
	id := s.relativeChannelIDLocked(delta)
	s.mu.Unlock()
	return s.playChannelID(id)
}

// relativeChannelIDLocked returns the ID of the catalog entry delta
// positions away from the current (or last played) channel, counting from
// the top when that is not in the catalog, and wrapping around; "" when the
// catalog is empty. It resolves the ID, not an index, in the caller's
// critical section: a catalog re-sort (a favorite toggled, a refresh)
// before Play re-locks would otherwise shift the index onto another
// channel. Caller holds s.mu.
func (s *Server) relativeChannelIDLocked(delta int) string {
	n := len(s.catalog)
	if n == 0 {
		return ""
	}
	idx := max(0, slices.IndexFunc(s.catalog, func(ch channels.Channel) bool { return ch.ID == s.channelID }))
	return s.catalog[((idx+delta)%n+n)%n].ID
}

// playChannelID plays id as resolved by relativeChannelIDLocked; "" means
// the catalog is empty.
func (s *Server) playChannelID(id string) (protocol.PlaybackState, error) {
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
	s.connectErr = nil
	s.connectTitle = ""
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
	// Negated so NaN (from an MPRIS client; it fails every comparison) ends
	// up at 0 too: it would otherwise break encoding every state event and
	// the state file.
	if !(v >= 0) {
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
	if ti.Gen != s.playGen {
		return
	}
	switch s.status {
	case protocol.StatusPlaying:
		s.publishTrackLocked(ti.Title)
	case protocol.StatusConnecting:
		// The session the player just committed reported its first title
		// before commitPlay got the lock; commitPlay publishes the latest.
		s.connectTitle = ti.Title
	}
}

// publishTrackLocked makes title the playing track: history, MPRIS, the
// state broadcast (whose snapshot it returns), the desktop notification,
// and Last.fm. Caller holds s.mu.
func (s *Server) publishTrackLocked(title string) protocol.PlaybackState {
	s.trackTitle = title
	s.recordHistoryLocked(s.channelID, s.channelTitle, title)
	s.updateMPRISLocked()
	snap := s.broadcastStateLocked()
	s.notifyTrackLocked()
	// Ends the previous title's pending scrobble (if it played long enough)
	// and starts tracking/now-playing the new one, or resumes the same play
	// when a reconnect re-reported it; see lastfm.go.
	s.updateLastfmLocked(s.channelID, title)
	return snap
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
