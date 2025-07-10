package main

import (
	"fmt"
	"io"
	"net/http"

	"github.com/hajimehoshi/ebiten/v2/audio/mp3"
	"github.com/hajimehoshi/oto/v2"
)

type Player struct {
	ctx    *oto.Context
	player oto.Player
	stream io.ReadCloser
}

func NewPlayer() (*Player, error) {
	ctx, ready, err := oto.NewContext(44100, 2, oto.FormatSignedInt16LE)
	if err != nil {
		return nil, fmt.Errorf("failed to create oto context: %w", err)
	}
	<-ready

	return &Player{ctx: ctx}, nil
}

func (p *Player) Play(url string) error {
	if p.player != nil {
		p.player.Close()
	}
	if p.stream != nil {
		p.stream.Close()
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	decodedStream, err := mp3.DecodeWithSampleRate(44100, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to decode mp3: %w", err)
	}

	p.stream = resp.Body
	p.player = p.ctx.NewPlayer(decodedStream)
	p.player.Play()

	return nil
}

func (p *Player) Stop() {
	if p.player != nil {
		p.player.Close()
	}
	if p.stream != nil {
		p.stream.Close()
	}
}