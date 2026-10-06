package main

import "somad/internal/ui"

// visualizerSaver remembers the TUI's visualizer style off the UI loop.
// Only the newest style waits to be written, so a burst of v presses
// costs at most two writes and never leaves an older style on disk last.
type visualizerSaver struct {
	pending chan ui.VisualizerMode
	done    chan struct{}
	save    func(ui.VisualizerMode) error
	onErr   func(error)
}

// newVisualizerSaver starts a saver that writes styles with save and hands
// failures to onErr.
func newVisualizerSaver(save func(ui.VisualizerMode) error, onErr func(error)) *visualizerSaver {
	s := &visualizerSaver{
		pending: make(chan ui.VisualizerMode, 1),
		done:    make(chan struct{}),
		save:    save,
		onErr:   onErr,
	}
	go s.loop()
	return s
}

// Offer queues mode to be written, replacing one still waiting. It never
// blocks; call it from one goroutine only (the TUI's update loop).
func (s *visualizerSaver) Offer(mode ui.VisualizerMode) {
	select {
	case <-s.pending:
	default:
	}
	s.pending <- mode
}

func (s *visualizerSaver) loop() {
	defer close(s.done)
	for mode := range s.pending {
		if err := s.save(mode); err != nil {
			s.onErr(err)
		}
	}
}

// Close writes the style still waiting, if any, and stops the saver. No
// Offer may follow.
func (s *visualizerSaver) Close() {
	close(s.pending)
	<-s.done
}
