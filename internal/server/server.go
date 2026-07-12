// Package server implements the soma daemon: it owns audio playback, the
// channel catalog, persisted state, and MPRIS, and serves clients over a
// Unix domain socket using the internal/protocol wire format.
package server

import (
	"fmt"
	"log"
	"net"
	"slices"
	"sync"
	"time"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/platform"
	"somad/internal/platform/tray"
	"somad/internal/protocol"
	"somad/internal/state"
)

// DefaultIdleTimeout is how long the server lingers with no connected
// clients and stopped playback before exiting on its own; 0 disables the
// idle exit, so by default the server runs until stopped explicitly.
const DefaultIdleTimeout time.Duration = 0

// channelRefreshInterval is a variable so tests can shrink it.
var channelRefreshInterval = 10 * time.Minute

// Config carries the dependencies for a Server.
type Config struct {
	Version     string
	UserAgent   string
	Player      audio.Player
	State       *state.State
	MPRIS       *platform.MPRIS // may be nil
	Tray        *tray.Tray      // may be nil
	IdleTimeout time.Duration   // 0 disables idle exit
	// PSK, when non-empty, is the pre-shared key every non-local (TCP)
	// connection must authenticate with before hello. Unix-socket
	// connections are exempt: the socket directory's permissions already
	// restrict them to the owning user.
	PSK string
}

// Server is the soma daemon. All mutable fields are guarded by mu; the
// audio player and state file are only touched through methods that hold it.
type Server struct {
	version     string
	userAgent   string
	player      audio.Player
	st          *state.State
	mpris       *platform.MPRIS
	tray        *tray.Tray
	idleTimeout time.Duration
	psk         string

	// persist writes user state to disk. It defaults to state.SaveState;
	// tests override it to avoid fsync-heavy disk writes on every mutation.
	persist func(*state.State) error

	// saveMu serializes persist calls so concurrent saves never interleave on
	// disk; savedSeq (guarded by saveMu) is the highest mutation sequence
	// already written, so a slow save can be dropped once a newer one lands.
	// dirty (also guarded by saveMu) is the newest state whose persist
	// failed, retried at shutdown so a transient write error with no further
	// mutation does not lose the state.
	saveMu   sync.Mutex
	savedSeq uint64
	dirty    *state.State

	shutdownOnce sync.Once
	done         chan struct{} // closed by Shutdown

	mu               sync.Mutex
	lns              []net.Listener
	conns            map[*conn]struct{}
	closing          bool
	catalog          []channels.Channel // favorites-first order
	catalogErr       string             // load failure while the catalog is empty
	status           string
	channelID        string // active channel while not stopped
	channelTitle     string
	trackTitle       string
	streamErr        string
	reconnectAttempt int
	playGen          uint64 // bumped by every play/stop; stale async work backs out
	saveSeq          uint64 // bumped per state mutation; orders persist writes
	reconnectTimer   *time.Timer
	idleTimer        *time.Timer
}

// New creates a Server and applies the persisted volume to the player.
func New(cfg Config) *Server {
	s := &Server{
		version:     cfg.Version,
		userAgent:   cfg.UserAgent,
		player:      cfg.Player,
		st:          cfg.State,
		mpris:       cfg.MPRIS,
		tray:        cfg.Tray,
		idleTimeout: cfg.IdleTimeout,
		psk:         cfg.PSK,
		persist:     state.SaveState,
		done:        make(chan struct{}),
		conns:       make(map[*conn]struct{}),
		status:      protocol.StatusStopped,
	}
	s.player.SetVolume(cfg.State.GetVolume())
	// MPRIS Play with no prior play in this process targets the last-played
	// channel from the previous session.
	s.channelID = cfg.State.LastSelectedChannelID
	if s.mpris != nil {
		s.mpris.SetSender(mprisSender{s})
		s.mpris.SetVolume(cfg.State.GetVolume())
	}
	if s.tray != nil {
		// The tray reuses the MPRIS command router: its menu items map onto
		// the same PlayPause/Next/Prev/Stop messages.
		s.tray.SetSender(mprisSender{s})
		s.tray.SetOnQuit(s.Shutdown)
	}
	return s
}

// Run serves connections on the given listeners (typically the Unix socket,
// plus a TCP listener when remote access is configured) until Shutdown is
// called (by a client request, a signal, or the idle timer). It owns the
// catalog load and the goroutines that watch the audio player.
func (s *Server) Run(lns ...net.Listener) error {
	s.mu.Lock()
	s.lns = lns
	// If no client ever connects (e.g. the spawning client died), the idle
	// timer still reaps the server.
	s.maybeArmIdleLocked()
	s.mu.Unlock()

	go s.watchPlayerErrors()
	go s.watchTrackUpdates()
	go s.refreshLoop()
	s.loadCatalog()

	errCh := make(chan error, len(lns))
	for _, ln := range lns {
		go func(ln net.Listener) { errCh <- s.acceptLoop(ln) }(ln)
	}
	var firstErr error
	for range lns {
		if err := <-errCh; err != nil && firstErr == nil {
			// One listener failing takes the server down; Shutdown unblocks
			// the remaining accept loops.
			firstErr = err
			s.Shutdown()
		}
	}
	return firstErr
}

// acceptLoop serves one listener until it closes; the error is nil when the
// close came from Shutdown.
func (s *Server) acceptLoop(ln net.Listener) error {
	for {
		nc, err := ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				return err
			}
		}
		go s.serveConn(nc)
	}
}

// Shutdown stops playback and tears the server down. Safe to call from any
// goroutine, multiple times.
func (s *Server) Shutdown() {
	s.shutdownOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.cancelReconnectLocked()
		s.disarmIdleLocked()
		lns := s.lns
		open := make([]*conn, 0, len(s.conns))
		for c := range s.conns {
			open = append(open, c)
		}
		s.mu.Unlock()

		s.player.Stop()
		if s.mpris != nil {
			s.mpris.Close()
		}
		if s.tray != nil {
			s.tray.Quit()
		}
		close(s.done)
		for _, ln := range lns {
			_ = ln.Close()
		}
		for _, c := range open {
			c.close()
		}
		s.flushDirtyState()
	})
}

// Done is closed when the server has shut down.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// watchPlayerErrors turns async stream errors into reconnect scheduling.
func (s *Server) watchPlayerErrors() {
	errs := s.player.Errors()
	for {
		select {
		case <-s.done:
			return
		case err, ok := <-errs:
			if !ok {
				return
			}
			if err != nil {
				s.handleStreamError(err)
			}
		}
	}
}

// watchTrackUpdates publishes now-playing titles demuxed from the stream.
func (s *Server) watchTrackUpdates() {
	updates := s.player.TrackUpdates()
	for {
		select {
		case <-s.done:
			return
		case ti, ok := <-updates:
			if !ok {
				return
			}
			s.handleTrackUpdate(ti)
		}
	}
}

// refreshLoop refreshes the channel catalog periodically.
func (s *Server) refreshLoop() {
	ticker := time.NewTicker(channelRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.refreshCatalog()
		}
	}
}

// loadCatalog seeds the catalog from the disk cache, then refreshes from the
// network in the background.
func (s *Server) loadCatalog() {
	if chs, err := channels.ReadChannelsFromCache(); err == nil {
		s.setCatalog(chs.Channels)
	}
	go s.refreshCatalog()
}

// refreshCatalog fetches the catalog from the network. Failures are silent
// while a previous catalog exists (background refresh), but surfaced to
// clients when there is nothing to show at all.
func (s *Server) refreshCatalog() {
	chs, err := channels.FetchChannelsFromNetwork(s.userAgent)
	if err != nil {
		log.Printf("channel refresh failed: %v", err)
		s.mu.Lock()
		if len(s.catalog) == 0 {
			s.catalogErr = err.Error()
			s.broadcastChannelsLocked()
		}
		s.mu.Unlock()
		return
	}
	s.setCatalog(chs.Channels)
}

func (s *Server) setCatalog(chs []channels.Channel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalog = sortChannelsWithFavorites(chs, s.st.FavoriteChannelIDs)
	s.catalogErr = ""
	s.broadcastChannelsLocked()
}

// sortChannelsWithFavorites returns the channels with favorites first, both
// groups keeping their relative order.
func sortChannelsWithFavorites(chs []channels.Channel, favorites []string) []channels.Channel {
	fav := make(map[string]bool, len(favorites))
	for _, id := range favorites {
		fav[id] = true
	}
	sorted := make([]channels.Channel, 0, len(chs))
	for _, ch := range chs {
		if fav[ch.ID] {
			sorted = append(sorted, ch)
		}
	}
	for _, ch := range chs {
		if !fav[ch.ID] {
			sorted = append(sorted, ch)
		}
	}
	return sorted
}

func (s *Server) findChannelLocked(id string) (channels.Channel, bool) {
	for _, ch := range s.catalog {
		if ch.ID == id {
			return ch, true
		}
	}
	return channels.Channel{}, false
}

// Snapshot returns the current playback state.
func (s *Server) Snapshot() protocol.PlaybackState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Server) snapshotLocked() protocol.PlaybackState {
	ps := protocol.PlaybackState{
		Status:      s.status,
		Volume:      s.player.Volume(),
		StreamError: s.streamErr,
	}
	if s.status != protocol.StatusStopped {
		ps.ChannelID = s.channelID
		ps.ChannelTitle = s.channelTitle
		ps.TrackTitle = s.trackTitle
	}
	if s.status == protocol.StatusReconnecting {
		ps.ReconnectAttempt = s.reconnectAttempt
	}
	return ps
}

// ChannelsPayload returns the catalog with the persisted per-user data.
func (s *Server) ChannelsPayload() protocol.ChannelsPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.channelsPayloadLocked()
}

func (s *Server) channelsPayloadLocked() protocol.ChannelsPayload {
	return protocol.ChannelsPayload{
		Channels: s.catalog,
		// Clone: ToggleFavorite mutates this slice in place under the lock,
		// while the exported ChannelsPayload path marshals the result after
		// releasing it. s.catalog is only ever replaced wholesale, so it needs
		// no copy.
		Favorites:     slices.Clone(s.st.FavoriteChannelIDs),
		LastChannelID: s.st.LastSelectedChannelID,
		Error:         s.catalogErr,
	}
}

// ToggleFavorite flips a channel's favorite flag, persists it, re-sorts the
// catalog, and notifies all clients.
func (s *Server) ToggleFavorite(channelID string) ([]string, error) {
	s.mu.Lock()
	if _, ok := s.findChannelLocked(channelID); !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("unknown channel: %s", channelID)
	}
	s.st.ToggleFavorite(channelID)
	stateToSave := s.st.Clone()
	saveSeq := s.nextSaveSeqLocked()
	s.catalog = sortChannelsWithFavorites(s.catalog, s.st.FavoriteChannelIDs)
	s.broadcastChannelsLocked()
	// Clone: the caller marshals this after the lock is released, but a later
	// ToggleFavorite mutates the underlying slice in place.
	favorites := slices.Clone(s.st.FavoriteChannelIDs)
	s.mu.Unlock()

	s.saveState(saveSeq, stateToSave)
	return favorites, nil
}

// nextSaveSeqLocked stamps a state mutation with a monotonic sequence so
// saveState can serialize writes and drop out-of-order ones. Caller holds s.mu.
func (s *Server) nextSaveSeqLocked() uint64 {
	s.saveSeq++
	return s.saveSeq
}

// saveState persists st, serialized against other saves so writes never
// interleave. seq is captured under s.mu when st was cloned; a save whose seq
// is not newer than the last one written is dropped, so a slow save can never
// clobber the state produced by a newer mutation.
func (s *Server) saveState(seq uint64, st *state.State) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if seq <= s.savedSeq {
		return
	}
	// savedSeq advances even when the write below fails: winding it back
	// would let a slower, older save proceed later and clobber this newer
	// state. The failed state is kept as dirty for the shutdown retry
	// instead; any newer successful save supersedes it.
	s.savedSeq = seq
	if err := s.persist(st); err != nil {
		log.Printf("error saving state: %v", err)
		s.dirty = st
		return
	}
	s.dirty = nil
}

// flushDirtyState retries the newest state save that failed, so a transient
// persist error does not carry user state loss across a shutdown.
func (s *Server) flushDirtyState() {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	if s.dirty == nil {
		return
	}
	if err := s.persist(s.dirty); err != nil {
		log.Printf("error saving state on shutdown: %v", err)
		return
	}
	s.dirty = nil
}

// addConn registers a connection; a live client keeps the server alive.
func (s *Server) addConn(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[c] = struct{}{}
	s.disarmIdleLocked()
	return true
}

func (s *Server) removeConn(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
	s.maybeArmIdleLocked()
}

// maybeArmIdleLocked starts the idle-exit countdown when nothing keeps the
// server alive: no clients and stopped playback.
func (s *Server) maybeArmIdleLocked() {
	if s.idleTimeout <= 0 || s.closing {
		return
	}
	if len(s.conns) > 0 || s.status != protocol.StatusStopped {
		return
	}
	s.disarmIdleLocked()
	s.idleTimer = time.AfterFunc(s.idleTimeout, func() {
		s.mu.Lock()
		idle := len(s.conns) == 0 && s.status == protocol.StatusStopped && !s.closing
		s.mu.Unlock()
		if idle {
			log.Printf("idle for %s, exiting", s.idleTimeout)
			s.Shutdown()
		}
	})
}

func (s *Server) disarmIdleLocked() {
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
}

// broadcastStateLocked pushes the current playback snapshot to all clients.
func (s *Server) broadcastStateLocked() {
	ev, err := protocol.NewEvent(protocol.EventState, s.snapshotLocked())
	if err != nil {
		log.Printf("error encoding state event: %v", err)
		return
	}
	for c := range s.conns {
		c.sendEvent(ev)
	}
}

// broadcastChannelsLocked pushes the catalog payload to all clients and mirrors
// it into the tray's channel picker.
func (s *Server) broadcastChannelsLocked() {
	s.pushChannelsToTrayLocked()
	ev, err := protocol.NewEvent(protocol.EventChannels, s.channelsPayloadLocked())
	if err != nil {
		log.Printf("error encoding channels event: %v", err)
		return
	}
	for c := range s.conns {
		c.sendEvent(ev)
	}
}

// pushChannelsToTrayLocked hands the current favorites-first catalog to the
// tray's channel picker.
func (s *Server) pushChannelsToTrayLocked() {
	if s.tray == nil {
		return
	}
	fav := make(map[string]bool, len(s.st.FavoriteChannelIDs))
	for _, id := range s.st.FavoriteChannelIDs {
		fav[id] = true
	}
	list := make([]tray.Channel, len(s.catalog))
	for i, ch := range s.catalog {
		list[i] = tray.Channel{ID: ch.ID, Title: ch.Title, Favorite: fav[ch.ID]}
	}
	s.tray.SetChannels(list)
}
