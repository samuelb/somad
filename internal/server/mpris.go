package server

import (
	"errors"

	"somad/internal/platform"
	"somad/internal/protocol"
)

// mprisSender routes incoming MPRIS commands (desktop media keys, applets)
// into the server. Play-ish commands run on their own goroutine because they
// block on the network and must not stall the D-Bus dispatcher.
type mprisSender struct {
	s *Server
}

func (m mprisSender) Send(msg any) {
	switch v := msg.(type) {
	case platform.MPRISPlayMsg:
		go func() { _, _ = m.s.PlayCurrent() }()
	case platform.MPRISStopMsg:
		m.s.Stop()
	case platform.MPRISPlayPauseMsg:
		go func() { _, _ = m.s.PlayPause() }()
	case platform.PlayChannelMsg:
		go func() { _, _ = m.s.Play(v.ID) }()
	case platform.ToggleFavoriteMsg:
		// Not network-bound, but ToggleFavorite writes the state file and
		// re-renders the tray menu; off-goroutine so the tray's click
		// dispatcher (which drops clicks it can't deliver) isn't blocked.
		go func() { _, _ = m.s.ToggleFavorite(v.ID) }()
	case platform.MPRISNextMsg:
		go func() { _, _ = m.s.PlayRelative(1) }()
	case platform.MPRISPrevMsg:
		go func() { _, _ = m.s.PlayRelative(-1) }()
	case platform.MPRISVolumeMsg:
		// The MPRIS property is already updated, so don't mirror it back.
		m.s.SetVolume(v.Volume, false)
	case platform.MPRISQuitMsg:
		// Off-goroutine: Shutdown tears down D-Bus among other things and
		// must not deadlock the dispatcher that delivered this message.
		go m.s.Shutdown()
	}
}

// PlayCurrent plays the last-played channel (falling back to the top of the
// catalog) unless something is already playing or connecting, in which case
// it is a no-op.
func (s *Server) PlayCurrent() (protocol.PlaybackState, error) {
	s.mu.Lock()
	if s.status != protocol.StatusStopped {
		snap := s.snapshotLocked()
		s.mu.Unlock()
		return snap, nil
	}
	id := s.channelID
	if _, ok := s.findChannelLocked(id); !ok {
		id = ""
		if len(s.catalog) > 0 {
			id = s.catalog[0].ID
		}
	}
	s.mu.Unlock()
	if id == "" {
		return s.Snapshot(), errors.New("no channels loaded")
	}
	return s.Play(id)
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
