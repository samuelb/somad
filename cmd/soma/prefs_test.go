package main

import (
	"errors"
	"sync"
	"testing"

	"somad/internal/ui"

	"github.com/stretchr/testify/assert"
)

func TestVisualizerSaver_NewestStyleWinsAndCloseFlushes(t *testing.T) {
	var mu sync.Mutex
	var saved []ui.VisualizerMode
	release := make(chan struct{})
	s := newVisualizerSaver(func(m ui.VisualizerMode) error {
		<-release // hold the first write while more styles arrive
		mu.Lock()
		saved = append(saved, m)
		mu.Unlock()
		return nil
	}, func(err error) { t.Errorf("unexpected error: %v", err) })

	s.Offer(ui.VisualizerBars)
	// However far the first write got, the burst behind it collapses to
	// its newest style.
	for _, m := range []ui.VisualizerMode{ui.VisualizerMirror, ui.VisualizerWave, ui.VisualizerWaterfall} {
		s.Offer(m)
	}
	close(release)
	s.Close()

	assert.Equal(t, ui.VisualizerWaterfall, saved[len(saved)-1], "the last style is the one on disk")
	assert.LessOrEqual(t, len(saved), 2)
}

func TestVisualizerSaver_ReportsFailures(t *testing.T) {
	var got error
	s := newVisualizerSaver(func(ui.VisualizerMode) error { return errors.New("disk full") },
		func(err error) { got = err })
	s.Offer(ui.VisualizerBars)
	s.Close()
	assert.EqualError(t, got, "disk full")
}
