package server

import "somad/internal/platform"

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
		// Never applied here: godbus runs the Volume property's write
		// callback while holding its property lock, which the server takes
		// in turn (mirroring state to MPRIS) while holding s.mu, so a
		// synchronous SetVolume deadlocks against any concurrent playback
		// change. See queueMPRISVolume.
		m.s.queueMPRISVolume(v.Volume)
	case platform.MPRISQuitMsg:
		// Off-goroutine: Shutdown tears down D-Bus among other things and
		// must not deadlock the dispatcher that delivered this message.
		go m.s.Shutdown()
	}
}

// queueMPRISVolume hands an MPRIS volume write to applyMPRISVolumes without
// blocking, replacing a value the worker has not picked up yet: while a
// desktop slider is dragged only its newest position matters. A single
// worker applies the values in the order they arrived (godbus serializes
// the property writes that call this), so the last slider position always
// wins.
func (s *Server) queueMPRISVolume(v float64) {
	for {
		select {
		case s.mprisVolume <- v:
			return
		default:
		}
		// The slot holds an older value; drop it, unless the worker has
		// just taken it, and try again.
		select {
		case <-s.mprisVolume:
		default:
		}
	}
}

// applyMPRISVolumes is the worker queueMPRISVolume feeds; it runs from New
// until Shutdown.
func (s *Server) applyMPRISVolumes() {
	for {
		select {
		case <-s.done:
			return
		case v := <-s.mprisVolume:
			// Shutdown wins over a value that was ready at the same time.
			select {
			case <-s.done:
				return
			default:
			}
			// The MPRIS property is already updated, so don't mirror it back.
			s.SetVolume(v, false)
		}
	}
}
