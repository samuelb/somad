package server

import (
	"fmt"
	"log"
	"time"

	"somad/internal/protocol"
)

// spectrumInterval is the time (in nanoseconds) between spectrum frames
// for subscribed connections: 25 a second, smooth enough for bars that
// fall under their own gravity in the TUI. Atomic so tests can shrink it
// without racing a frame loop an earlier test left winding down.
var spectrumInterval = newDurationAtomic(40 * time.Millisecond)

// spectrumTail is how many silent frames follow the end of playback, so
// the clients' bars settle to the floor instead of freezing mid-air.
const spectrumTail = 25

// SubscribeSpectrum sets how many bands c's spectrum events carry, 0 to
// stop them, and starts the frame loop for the first subscriber.
func (s *Server) SubscribeSpectrum(c *conn, bands int) error {
	if bands < 0 || bands > protocol.MaxSpectrumBands {
		return fmt.Errorf("spectrum bands must be between 0 and %d, got %d", protocol.MaxSpectrumBands, bands)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.spectrumBands = bands
	if bands > 0 && !s.spectrumRunning && !s.closing {
		s.spectrumRunning = true
		go s.spectrumLoop()
	}
	return nil
}

// spectrumLoop sends spectrum frames to the subscribed connections until
// none is left or the server shuts down. While nothing plays it still
// ticks, but sends nothing once the tail of silent frames is out.
func (s *Server) spectrumLoop() {
	t := time.NewTicker(time.Duration(spectrumInterval.Load()))
	defer t.Stop()
	silent := spectrumTail // nothing has played yet: no tail to send
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
		}
		// The player locks itself; the analysis must not hold up mu. A
		// stop between the two calls lets one last frame of the old
		// session out, which the silent tail then overwrites.
		spec, ok := s.player.Spectrum()
		subs := s.spectrumSubscribers()
		if subs == nil {
			return
		}
		if ok {
			silent = 0
		} else {
			silent++
			if silent > spectrumTail {
				continue
			}
		}
		for c, bands := range subs {
			ev, err := protocol.NewEvent(protocol.EventSpectrum, protocol.SpectrumEvent{Levels: spec.Bands(bands)})
			if err != nil {
				log.Printf("error encoding %s event: %v", protocol.EventSpectrum, err)
				continue
			}
			c.sendEvent(ev)
		}
	}
}

// spectrumSubscribers returns the subscribed connections and their band
// counts. With none left it returns nil and marks the loop stopped, under
// the same lock SubscribeSpectrum starts one under, so a subscriber never
// goes without a loop.
func (s *Server) spectrumSubscribers() map[*conn]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var subs map[*conn]int
	for c := range s.conns {
		if c.spectrumBands > 0 {
			if subs == nil {
				subs = make(map[*conn]int)
			}
			subs[c] = c.spectrumBands
		}
	}
	if subs == nil {
		s.spectrumRunning = false
	}
	return subs
}
