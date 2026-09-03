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
	s.lastfmTrack = &lastfmTrack{artist: artist, title: title, startedAt: time.Now()}
	scrobbler := s.scrobbler
	s.submitLastfm("now-playing", func() error { return scrobbler.UpdateNowPlaying(artist, title) })
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
	s.submitLastfm("scrobble", func() error { return scrobbler.Scrobble(tr.artist, tr.title, tr.startedAt) })
}

// submitLastfm runs action (an UpdateNowPlaying or Scrobble call) on its own
// goroutine, off the playback hot path, retrying once after a short delay
// on failure. kind names the call for the log line ("now-playing" or
// "scrobble"); a failure is logged once per kind, further ones of the same
// kind silently swallowed — like desktop notifications (ADR-0030), this is
// a nice-to-have, never worth playback going wrong over.
func (s *Server) submitLastfm(kind string, action func() error) {
	go func() {
		if err := action(); err != nil {
			time.Sleep(lastfmRetryDelay)
			if err = action(); err != nil {
				s.logLastfmFailureOnce(kind, err)
			}
		}
	}()
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
	s.mu.Lock()
	scrobbler := s.scrobbler
	reload := s.reloadLastfmSession
	s.mu.Unlock()
	if scrobbler == nil || reload == nil {
		return nil
	}
	key, err := reload()
	if err != nil {
		return err
	}
	scrobbler.SetSessionKey(key)
	return nil
}
