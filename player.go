package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/ebitengine/oto/v3"
	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36"

// Player manages the audio playback for SomaFM streams.
type Player struct {
	ctx    *oto.Context
	player *oto.Player
	stream io.Closer
}

// NewPlayer initializes a new audio player with a default sample rate and channel count.
func NewPlayer() (*Player, error) {
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

	return &Player{ctx: ctx}, nil
}

// Play starts streaming and playing audio from the given URL.
// It closes any previously playing stream before starting a new one.
func (p *Player) Play(url string) error {
	// Close any existing player and stream to prevent resource leaks
	if p.player != nil {
		p.player = nil
	}
	if p.stream != nil {
		_ = p.stream.Close()
		p.stream = nil
	}

	// Create a pipe to connect the HTTP stream to the MP3 decoder
	pr, pw := io.Pipe()

	// Start a goroutine to fetch the stream and write it to the pipe
	go func() {
		defer func() { _ = pw.Close() }()

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("failed to create request: %w", err))
			return
		}
		req.Header.Set("User-Agent", userAgent)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			pw.CloseWithError(fmt.Errorf("failed to fetch stream: %w", err))
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			pw.CloseWithError(fmt.Errorf("unexpected status code: %d", resp.StatusCode))
			return
		}

		// Copy the stream to the pipe writer
		_, err = io.Copy(pw, resp.Body)
		if err != nil {
			// An error is expected on pipe close, so we don't report it
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
	p.player.Play()

	return nil
}

// Stop halts the current audio playback and closes the associated stream.
func (p *Player) Stop() {
	if p.player != nil {
		p.player = nil
	}
	if p.stream != nil {
		_ = p.stream.Close()
		p.stream = nil
	}
}

