package audio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"somatui/internal/security"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
)

const (
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

// AudioPlayer manages the audio playback for SomaFM streams.
type AudioPlayer struct {
	ctx        *oto.Context
	player     *oto.Player
	stream     io.Closer
	cancelFade chan struct{}
	reqCancel  context.CancelFunc
	userAgent  string
	errChan    chan error
}

// NewPlayer initializes a new audio player with a default sample rate and channel count.
func NewPlayer(userAgent string) (*AudioPlayer, error) {
	// Initialize oto context with standard audio parameters
	op := &oto.NewContextOptions{
		SampleRate:   44100,
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

// Play starts streaming and playing audio from the given URL.
// It closes any previously playing stream before starting a new one.
func (p *AudioPlayer) Play(url string) error {
	// Cancel any ongoing fade-in and fade out current playback
	if p.cancelFade != nil {
		close(p.cancelFade)
	}
	p.fadeOut()
	p.cancelFade = make(chan struct{})
	p.cleanup()

	// Create a pipe to connect the HTTP stream to the MP3 decoder
	pr, pw := io.Pipe()

	// Start a goroutine to fetch the stream and write it to the pipe
	ctx, cancel := context.WithCancel(context.Background())
	p.reqCancel = cancel

	go func() {
		defer func() { _ = pw.Close() }()
		defer cancel()

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

		// Copy the stream to the pipe writer
		_, err = io.Copy(pw, resp.Body)
		if err != nil {
			// An error is expected on pipe close, so we don't report it
			if ctx.Err() == nil {
				p.reportError(ctx, fmt.Errorf("stream read error: %w", err))
			}
			return
		}
	}()

	// Decode the MP3 stream from the pipe reader
	decodedStream, err := mp3.DecodeWithSampleRate(44100, pr)
	if err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return fmt.Errorf("failed to decode mp3: %w", err)
	}

	// Store the pipe reader (for closing) and create a new player, then start playback
	p.stream = pr
	p.player = p.ctx.NewPlayer(decodedStream)
	p.player.SetVolume(0)
	p.player.Play()

	// Start fade-in goroutine
	go p.fadeIn()

	return nil
}

// Errors returns a channel for async stream errors.
func (p *AudioPlayer) Errors() <-chan error {
	return p.errChan
}

// fadeIn gradually increases the volume from 0 to 1.
func (p *AudioPlayer) fadeIn() {
	stepDuration := fadeInDuration / fadeSteps
	for i := 1; i <= fadeSteps; i++ {
		select {
		case <-p.cancelFade:
			return
		case <-time.After(stepDuration):
			if p.player != nil {
				p.player.SetVolume(float64(i) / fadeSteps)
			}
		}
	}
}

// fadeOut gradually decreases the volume from current to 0.
func (p *AudioPlayer) fadeOut() {
	if p.player == nil {
		return
	}
	stepDuration := fadeOutDuration / fadeSteps
	startVolume := p.player.Volume()
	for i := fadeSteps - 1; i >= 0; i-- {
		time.Sleep(stepDuration)
		if p.player != nil {
			p.player.SetVolume(startVolume * float64(i) / fadeSteps)
		}
	}
}

// Stop halts the current audio playback and closes the associated stream.
func (p *AudioPlayer) Stop() {
	// Cancel any ongoing fade-in and fade out
	if p.cancelFade != nil {
		close(p.cancelFade)
		p.cancelFade = nil
	}
	p.fadeOut()
	p.cleanup()
}

// cleanup releases player and stream resources.
func (p *AudioPlayer) cleanup() {
	if p.player != nil {
		p.player.Pause()
		p.player = nil
	}
	if p.stream != nil {
		_ = p.stream.Close()
		p.stream = nil
	}
	if p.reqCancel != nil {
		p.reqCancel()
		p.reqCancel = nil
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
