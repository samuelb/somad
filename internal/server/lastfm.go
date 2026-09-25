package server

import (
	"log"
	"time"

	"somad/internal/audio"
)

// Scrobbler is the Last.fm now-playing/scrobble sink (TODO.md "Last.fm
// scrobbling"); internal/lastfm.Client implements it against the real API.
// A nil Scrobbler (the default) disables the feature until a reloadLastfm
// finds it configured (Server.ReloadLastfm).
type Scrobbler interface {
	// UpdateNowPlaying tells Last.fm what is currently playing.
	UpdateNowPlaying(artist, title string) error
	// Scrobble records a completed track play that started at startedAt.
	Scrobble(artist, title string, startedAt time.Time) error
	// SetSessionKey updates the session key calls authenticate with,
	// without reconstructing the Scrobbler — see Server.ReloadLastfm.
	SetSessionKey(key string)
}

// lastfmMinPlayDuration is Last.fm's minimum playtime before a track
// qualifies for scrobbling ("the track must have been played for at least
// half its duration, or for 30 seconds, whichever comes first" —
// https://www.last.fm/api/scrobbling). Radio streams carry no track length,
// so only the 30 s floor applies here. A variable so tests can shrink it.
var lastfmMinPlayDuration = 30 * time.Second

// lastfmRetryDelay is how long the scrobble/now-playing goroutine waits
// before its one retry of a failed submission. A variable so tests can
// shrink it.
var lastfmRetryDelay = 10 * time.Second

// lastfmShutdownWait bounds how long Shutdown waits for in-flight Last.fm
// submissions (the final scrobble in particular) to finish, so a slow or
// unreachable Last.fm never delays shutdown indefinitely. A variable so
// tests can shrink it.
var lastfmShutdownWait = 3 * time.Second

// lastfmSameTrackWindow bounds how long after a track was first seen on a
// channel a re-appearance of the same artist/title on that channel still
// counts as the same play (see lastfmTrack). Radio tracks rarely run past
// an hour; two hours leaves room for the longest ambient sets while a
// genuine replay of the same track later in the day is still scrobbled
// again. A variable so tests can shrink it.
var lastfmSameTrackWindow = 2 * time.Hour

// lastfmTrack is one play of a track on a channel, from when its title was
// first seen until a different title follows it. Live radio has no seek or
// skip, so a pause, a stream drop and reconnect, or a switch to another
// channel and back all resume the same play rather than start a new one;
// each of those tears the stream down and the fresh connection re-reports
// the title, which is why the play is remembered per channel across the gap
// (Server.lastfmRecent) and matched by channel, artist, and title. played
// accumulates only the stretches actually listened to (resumedAt is the
// start of the current one), so a track that was paused after 10 s and
// resumed still needs 20 s more before it qualifies; startedAt, the first
// sighting, is the scrobble timestamp. scrobbled makes a play scrobble at
// most once, however many times it is interrupted afterwards.
type lastfmTrack struct {
	channelID     string
	artist, title string
	startedAt     time.Time
	resumedAt     time.Time
	played        time.Duration
	scrobbled     bool
}

func (tr *lastfmTrack) matches(channelID, artist, title string) bool {
	return tr.channelID == channelID && tr.artist == artist && tr.title == title
}

// updateLastfmLocked ends the previously tracked now-playing track (queuing
// it for a scrobble when it played long enough and was not scrobbled yet)
// and, when rawTitle splits into an artist and title (audio.SplitTitle; a
// title with no artist is skipped — Last.fm scrobbles need one), starts
// tracking it on channelID and sends a now-playing update. When it is the
// track last seen on that channel within lastfmSameTrackWindow, the
// remembered play resumes instead (same start time, listened time, and
// scrobbled flag), so an interruption never turns one play into two. No-op
// when scrobbling is not configured. Caller holds s.mu.
func (s *Server) updateLastfmLocked(channelID, rawTitle string) {
	if s.scrobbler == nil {
		return
	}
	s.endLastfmTrackLocked()

	artist, title := audio.SplitTitle(rawTitle)
	if artist == "" {
		return
	}
	now := time.Now()
	tr := s.lastfmRecent[channelID]
	if tr != nil && tr.matches(channelID, artist, title) && now.Sub(tr.startedAt) < lastfmSameTrackWindow {
		delete(s.lastfmRecent, channelID)
		tr.resumedAt = now
	} else {
		tr = &lastfmTrack{channelID: channelID, artist: artist, title: title, startedAt: now, resumedAt: now}
	}
	s.lastfmTrack = tr
	scrobbler := s.scrobbler
	// Captured so the retry can check, under s.mu, whether this is still the
	// track s.lastfmTrack points to: if a later title change has already
	// replaced it (which sends its own now-playing update), retrying this
	// stale one would incorrectly restamp Last.fm's "now playing" backward.
	stillCurrent := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.lastfmTrack == tr
	}
	s.submitLastfm("now-playing", stillCurrent, func() error { return scrobbler.UpdateNowPlaying(artist, title) })
}

// endLastfmTrackLocked ends the currently tracked now-playing track, if
// any: it banks the stretch just listened to, remembers the play as the
// last one seen on its channel (so a reconnect, unpause, or return to the
// channel can resume it, see lastfmTrack), and scrobbles it (off s.mu, on a
// goroutine) when it has now played at least lastfmMinPlayDuration in
// total and was not scrobbled before. Caller holds s.mu.
func (s *Server) endLastfmTrackLocked() {
	if s.scrobbler == nil || s.lastfmTrack == nil {
		return
	}
	tr := s.lastfmTrack
	s.lastfmTrack = nil
	tr.played += time.Since(tr.resumedAt)
	if s.lastfmRecent == nil {
		s.lastfmRecent = make(map[string]*lastfmTrack)
	}
	s.lastfmRecent[tr.channelID] = tr
	if tr.scrobbled || tr.played < lastfmMinPlayDuration {
		return
	}
	// Marked before the submission goes out: a play is scrobbled once,
	// whether or not Last.fm accepted it. Retrying on a later interruption
	// would risk the duplicate this flag exists to prevent, and submitLastfm
	// already retries a failure once itself.
	tr.scrobbled = true
	scrobbler := s.scrobbler
	// No latest-wins check: unlike a now-playing update, each scrobble
	// records a distinct historical play, so a retry of an older one is
	// never invalidated by a newer one.
	s.submitLastfm("scrobble", nil, func() error { return scrobbler.Scrobble(tr.artist, tr.title, tr.startedAt) })
}

// submitLastfm runs action (an UpdateNowPlaying or Scrobble call) on its own
// goroutine, off the playback hot path, retrying once after a short delay
// on failure. kind names the call for the log line ("now-playing" or
// "scrobble"). retryOK, when non-nil, is checked immediately before the
// retry; a false result skips it silently (see updateLastfmLocked). The
// goroutine is tracked in lastfmWG, which Shutdown waits on (bounded by
// lastfmShutdownWait) so the final scrobble has a chance to land; once the
// server is closing, a failed submission skips the retry delay entirely
// rather than outlive that bound for nothing. A failure is logged once per
// kind, further ones of the same kind silently swallowed — like desktop
// notifications (ADR-0030), this is a nice-to-have, never worth playback
// going wrong over.
func (s *Server) submitLastfm(kind string, retryOK func() bool, action func() error) {
	s.lastfmWG.Add(1)
	go func() {
		defer s.lastfmWG.Done()
		if err := action(); err != nil {
			if s.isClosing() {
				return
			}
			time.Sleep(lastfmRetryDelay)
			if retryOK != nil && !retryOK() {
				return
			}
			if err = action(); err != nil {
				s.logLastfmFailureOnce(kind, err)
			}
		}
	}()
}

// isClosing reports whether Shutdown has begun.
func (s *Server) isClosing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closing
}

// waitLastfmSubmissions waits (bounded by lastfmShutdownWait) for in-flight
// Last.fm submissions to finish, so the final scrobble Shutdown triggers has
// a chance to actually be sent before the process exits. A no-op when
// scrobbling is not configured.
func (s *Server) waitLastfmSubmissions() {
	s.mu.Lock()
	configured := s.scrobbler != nil
	s.mu.Unlock()
	if !configured {
		return
	}
	done := make(chan struct{})
	go func() {
		s.lastfmWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(lastfmShutdownWait):
	}
}

func (s *Server) logLastfmFailureOnce(kind string, err error) {
	s.lastfmLogMu.Lock()
	defer s.lastfmLogMu.Unlock()
	if s.lastfmLogged == nil {
		s.lastfmLogged = make(map[string]bool)
	}
	if s.lastfmLogged[kind] {
		return
	}
	s.lastfmLogged[kind] = true
	log.Printf("last.fm %s failed (further %s failures are not logged): %v", kind, kind, err)
}

// ReloadLastfm re-reads the Last.fm session key (the config's
// lastfm.session_key override, else internal/state's persisted
// lastfm.json — see Config.ReloadLastfmSession) and applies it to the
// running Scrobbler, so a session obtained by "soma lastfm login" after
// this daemon started takes effect without a restart. With no Scrobbler
// yet it first asks Config.LoadScrobbler for one, so credentials added to
// the config file after startup take effect the same way; a no-op while
// scrobbling is still not configured.
func (s *Server) ReloadLastfm() error {
	s.lastfmReloadMu.Lock()
	defer s.lastfmReloadMu.Unlock()
	s.mu.Lock()
	scrobbler := s.scrobbler
	s.mu.Unlock()

	installed := scrobbler != nil
	if !installed {
		if s.loadScrobbler == nil {
			return nil
		}
		var err error
		if scrobbler, err = s.loadScrobbler(); err != nil || scrobbler == nil {
			return err
		}
	}
	if s.reloadLastfmSession != nil {
		key, err := s.reloadLastfmSession()
		if err != nil {
			return err
		}
		scrobbler.SetSessionKey(key)
	}
	if !installed {
		// Picks up from the next title change on; the one playing now was
		// never tracked, so it is neither announced nor scrobbled.
		s.mu.Lock()
		s.scrobbler = scrobbler
		s.mu.Unlock()
	}
	return nil
}
