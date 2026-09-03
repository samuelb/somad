package server

import (
	"log"
	"time"

	"somad/internal/audio"
)

// Scrobbler is the Last.fm now-playing/scrobble sink (TODO.md "Last.fm
// scrobbling"); internal/lastfm.Client implements it against the real API.
// A nil Scrobbler (the default) disables the feature entirely.
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

// lastfmTrack is the now-playing track a future scrobble is pending for.
type lastfmTrack struct {
	artist, title string
	startedAt     time.Time
}

// updateLastfmLocked ends the previously tracked now-playing track (queuing
// it for a scrobble when it played long enough) and, when rawTitle splits
// into an artist and title (audio.SplitTitle; a title with no artist is
// skipped — Last.fm scrobbles need one), starts tracking the new one and
// sends a now-playing update. No-op when scrobbling is not configured.
// Caller holds s.mu.
func (s *Server) updateLastfmLocked(rawTitle string) {
	if s.scrobbler == nil {
		return
	}
	s.endLastfmTrackLocked()

	artist, title := audio.SplitTitle(rawTitle)
	if artist == "" {
		return
	}
	tr := &lastfmTrack{artist: artist, title: title, startedAt: time.Now()}
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
// any, scrobbling it (off s.mu, on a goroutine) when it played at least
// lastfmMinPlayDuration. Caller holds s.mu.
func (s *Server) endLastfmTrackLocked() {
	if s.scrobbler == nil || s.lastfmTrack == nil {
		return
	}
	tr := s.lastfmTrack
	s.lastfmTrack = nil
	if time.Since(tr.startedAt) < lastfmMinPlayDuration {
		return
	}
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
	if s.scrobbler == nil {
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
// this daemon started takes effect without a restart. A no-op when
// scrobbling is not configured at all.
func (s *Server) ReloadLastfm() error {
	// Both fields are set once in New and never written again, so no lock.
	if s.scrobbler == nil || s.reloadLastfmSession == nil {
		return nil
	}
	key, err := s.reloadLastfmSession()
	if err != nil {
		return err
	}
	s.scrobbler.SetSessionKey(key)
	return nil
}
