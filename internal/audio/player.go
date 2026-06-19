package audio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"somatui/internal/security"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
)

const (
	sampleRate      = 44100
	fadeInDuration  = 500 * time.Millisecond
	fadeOutDuration = 250 * time.Millisecond
	fadeSteps       = 20
)

// Player is the interface for audio playback operations.
// This allows mocking the player in tests.
type Player interface {
	Play(url string) error
	Stop()
	Errors() <-chan error
}

// session represents a single playback lifecycle: one stream, one decoder,
// one oto player. After creation, only its managing goroutine (runSession)
// touches the oto player, which keeps volume changes free of data races.
type session struct {
	player   *oto.Player
	stream   io.Closer
	cancel   context.CancelFunc // aborts the HTTP fetch goroutine
	stop     chan struct{}      // closed to request fade-out and teardown
	stopOnce sync.Once
}

// requestStop signals the session to fade out and release resources.
// Safe to call multiple times.
func (s *session) requestStop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// AudioPlayer manages the audio playback for SomaFM streams.
type AudioPlayer struct {
	ctx       *oto.Context
	userAgent string
	errChan   chan error

	mu      sync.Mutex
	current *session // the active session, guarded by mu
}

// NewPlayer initializes a new audio player with a default sample rate and channel count.
func NewPlayer(userAgent string) (*AudioPlayer, error) {
	// Initialize oto context with standard audio parameters
	op := &oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: 2,
		Format:       oto.FormatSignedInt16LE,
	}
	ctx, ready, err := oto.NewContext(op)
	if err != nil {
		return nil, fmt.Errorf("failed to create oto context: %w", err)
	}
	// Wait for the audio context to be ready
	<-ready

	return &AudioPlayer{ctx: ctx, userAgent: userAgent, errChan: make(chan error, 2)}, nil
}

// Play starts streaming and playing audio from the given URL. It returns once
// the stream is decoding and playback has begun; the previous session (if any)
// fades out and tears down asynchronously, so this never blocks the caller.
func (p *AudioPlayer) Play(url string) error {
	// Create a pipe to connect the HTTP stream to the MP3 decoder.
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())

	go p.fetchStream(ctx, url, pw)

	// Decode the MP3 stream from the pipe reader. This is the only synchronous
	// failure mode, so the new session is not committed until decoding succeeds.
	decodedStream, err := mp3.DecodeWithSampleRate(sampleRate, pr)
	if err != nil {
		cancel()
		_ = pr.Close()
		_ = pw.Close()
		return fmt.Errorf("failed to decode mp3: %w", err)
	}

	player := p.ctx.NewPlayer(decodedStream)
	player.SetVolume(0)
	player.Play()

	s := &session{
		player: player,
		stream: pr,
		cancel: cancel,
		stop:   make(chan struct{}),
	}

	// Swap in the new session and stop the old one (which fades out on its own
	// goroutine, briefly crossfading with the new stream for gapless switching).
	p.mu.Lock()
	old := p.current
	p.current = s
	p.mu.Unlock()

	if old != nil {
		old.requestStop()
	}

	go p.runSession(s)
	return nil
}

// fetchStream fetches the stream over HTTP and pipes it to the decoder.
// It reports network errors asynchronously via the errors channel.
func (p *AudioPlayer) fetchStream(ctx context.Context, url string, pw *io.PipeWriter) {
	defer func() { _ = pw.Close() }()

	req, err := security.NewRequest(ctx, url, p.userAgent)
	if err != nil {
		streamErr := fmt.Errorf("invalid stream URL: %w", err)
		p.reportError(ctx, streamErr)
		pw.CloseWithError(streamErr)
		return
	}

	client := &http.Client{}
	resp, err := client.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		streamErr := fmt.Errorf("failed to fetch stream: %w", err)
		p.reportError(ctx, streamErr)
		pw.CloseWithError(streamErr)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		streamErr := fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		p.reportError(ctx, streamErr)
		pw.CloseWithError(streamErr)
		return
	}

	// Copy the stream to the pipe writer until cancelled or the stream ends.
	if _, err := io.Copy(pw, resp.Body); err != nil {
		// An error is expected on cancellation/pipe close, so we don't report it.
		if ctx.Err() == nil {
			p.reportError(ctx, fmt.Errorf("stream read error: %w", err))
		}
	}
}

// Errors returns a channel for async stream errors.
func (p *AudioPlayer) Errors() <-chan error {
	return p.errChan
}

// runSession owns the session's oto player for its entire lifetime: it fades
// the volume in, holds until a stop is requested, then fades out and releases
// resources. Because only this goroutine touches s.player after Play, volume
// changes and teardown never race.
func (p *AudioPlayer) runSession(s *session) {
	if p.fadeIn(s) {
		// Fade-in completed without interruption; hold until asked to stop.
		<-s.stop
	}
	p.fadeOutAndClose(s)
}

// fadeIn gradually raises the session volume from 0 to 1. It returns true if
// the fade completed, or false if a stop was requested partway through.
func (p *AudioPlayer) fadeIn(s *session) bool {
	step := fadeInDuration / fadeSteps
	for i := 1; i <= fadeSteps; i++ {
		select {
		case <-s.stop:
			return false
		case <-time.After(step):
			s.player.SetVolume(float64(i) / fadeSteps)
		}
	}
	return true
}

// fadeOutAndClose gradually lowers the session volume to 0, then pauses the
// player, closes the stream, and cancels the HTTP fetch.
func (p *AudioPlayer) fadeOutAndClose(s *session) {
	step := fadeOutDuration / fadeSteps
	startVolume := s.player.Volume()
	for i := fadeSteps - 1; i >= 0; i-- {
		s.player.SetVolume(startVolume * float64(i) / fadeSteps)
		time.Sleep(step)
	}
	s.player.Pause()
	_ = s.stream.Close()
	s.cancel()
}

// Stop halts the current audio playback. The fade-out and teardown run
// asynchronously, so this returns immediately.
func (p *AudioPlayer) Stop() {
	p.mu.Lock()
	old := p.current
	p.current = nil
	p.mu.Unlock()

	if old != nil {
		old.requestStop()
	}
}

func (p *AudioPlayer) reportError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	select {
	case p.errChan <- err:
	default:
	}
}
