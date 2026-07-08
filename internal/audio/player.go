package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"somad/internal/security"

	"github.com/ebitengine/oto/v3"
	mp3 "github.com/hajimehoshi/go-mp3"
)

const (
	sampleRate      = 44100
	fadeInDuration  = 500 * time.Millisecond
	fadeOutDuration = 250 * time.Millisecond
	fadeSteps       = 20
)

// streamStallTimeout is how long the stream may deliver no data before the
// watchdog aborts it: a connection that dies without a FIN (lost link, NAT
// timeout) blocks reads forever and would otherwise never trigger
// reconnection. A variable so tests can shrink it.
var streamStallTimeout = 30 * time.Second

// ErrSuperseded is returned by Play when a newer Play or Stop request arrived
// while this one was still connecting; the newer request owns the audio state.
var ErrSuperseded = errors.New("playback superseded by a newer request")

// Player is the interface for audio playback operations.
// This allows mocking the player in tests.
type Player interface {
	Play(url string) error
	Stop()
	Errors() <-chan error
	TrackUpdates() <-chan TrackInfo
	SetVolume(v float64)
	Volume() float64
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
	volumeCh chan float64 // volume targets for the session goroutine to apply
}

// requestStop signals the session to fade out and release resources.
// Safe to call multiple times.
func (s *session) requestStop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

// setVolume hands a new volume target to the session goroutine, replacing any
// pending one so the newest value wins.
func (s *session) setVolume(v float64) {
	select {
	case <-s.volumeCh:
	default:
	}
	select {
	case s.volumeCh <- v:
	default:
	}
}

// AudioPlayer manages the audio playback for SomaFM streams.
type AudioPlayer struct {
	ctx       *oto.Context
	userAgent string
	errChan   chan error
	trackChan chan TrackInfo

	mu      sync.Mutex
	current *session // the active session, guarded by mu
	playGen uint64   // bumped by every Play/Stop so stale connects never commit
	volume  float64  // target volume in [0, 1], guarded by mu
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

	return &AudioPlayer{
		ctx:       ctx,
		userAgent: userAgent,
		errChan:   make(chan error, 2),
		trackChan: make(chan TrackInfo, 1),
		volume:    1,
	}, nil
}

// Play starts streaming and playing audio from the given URL. It blocks until
// the stream is decoding and playback has begun; the previous session (if any)
// fades out and tears down asynchronously. Play is safe to call concurrently:
// if another Play or Stop arrives while this one is still connecting, the
// newer request wins and this one returns ErrSuperseded without touching the
// audio state.
func (p *AudioPlayer) Play(url string) error {
	p.mu.Lock()
	p.playGen++
	gen := p.playGen
	p.mu.Unlock()

	// Create a pipe to connect the HTTP stream to the MP3 decoder.
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())

	discard := func() {
		cancel()
		_ = pr.Close()
		_ = pw.Close()
	}

	go p.fetchStream(ctx, url, pw)

	// Decode the MP3 stream from the pipe reader. This is the only synchronous
	// failure mode, so the new session is not committed until decoding succeeds.
	decoder, err := mp3.NewDecoder(pr)
	if err != nil {
		discard()
		return fmt.Errorf("failed to decode mp3: %w", err)
	}

	// The oto context runs at a fixed rate; resample if the stream differs.
	var decodedStream io.Reader = decoder
	if decoder.SampleRate() != sampleRate {
		decodedStream = newResampler(decoder, decoder.SampleRate(), sampleRate)
	}

	// Commit the new session and stop the old one (which fades out on its own
	// goroutine, briefly crossfading with the new stream for gapless switching).
	// If a newer Play/Stop arrived while we were connecting, back out instead.
	p.mu.Lock()
	if gen != p.playGen {
		p.mu.Unlock()
		discard()
		return ErrSuperseded
	}

	player := p.ctx.NewPlayer(decodedStream)
	player.SetVolume(0)
	player.Play()

	s := &session{
		player:   player,
		stream:   pr,
		cancel:   cancel,
		stop:     make(chan struct{}),
		volumeCh: make(chan float64, 1),
	}
	old := p.current
	p.current = s
	p.mu.Unlock()

	// Titles buffered from the previous channel must not leak into this one.
	p.drainTrackUpdates()

	if old != nil {
		old.requestStop()
	}

	go p.runSession(s)
	return nil
}

// fetchStream fetches the stream over HTTP and pipes it to the decoder. It
// requests interleaved ICY metadata so the same connection carries the
// now-playing titles, which are demuxed out and reported via TrackUpdates.
// Network errors are reported asynchronously via the errors channel.
func (p *AudioPlayer) fetchStream(ctx context.Context, url string, pw *io.PipeWriter) {
	defer func() { _ = pw.Close() }()

	// The watchdog aborts the request when the connection goes silent for
	// streamStallTimeout; reads on the body below re-arm it. It runs from
	// before the request so a server that never answers is caught too.
	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	var stalled atomic.Bool
	watchdog := time.AfterFunc(streamStallTimeout, func() {
		stalled.Store(true)
		cancelReq()
	})
	defer watchdog.Stop()
	// stallErr rewrites an error caused by the watchdog's own cancellation
	// into one that names the stall.
	stallErr := func(err error) error {
		if stalled.Load() {
			return fmt.Errorf("stream stalled: no data received for %s", streamStallTimeout)
		}
		return err
	}

	req, err := security.NewRequest(reqCtx, url, p.userAgent)
	if err != nil {
		streamErr := fmt.Errorf("invalid stream URL: %w", err)
		p.reportError(ctx, streamErr)
		pw.CloseWithError(streamErr)
		return
	}
	req.Header.Set("Icy-MetaData", "1") // Request interleaved ICY metadata

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		streamErr := stallErr(fmt.Errorf("failed to fetch stream: %w", err))
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

	// If the server honored the metadata request, demux titles out of the
	// stream; otherwise the body is pure audio and passes through untouched.
	var body io.Reader = &watchdogReader{r: resp.Body, timer: watchdog, timeout: streamStallTimeout}
	if icyInt, err := strconv.Atoi(resp.Header.Get("icy-metaint")); err == nil && icyInt > 0 {
		body = newICYDemuxer(body, icyInt, func(title string) {
			p.reportTrack(ctx, TrackInfo{Title: title})
		})
	}

	// Copy the stream to the pipe writer until cancelled or the stream ends.
	_, err = io.Copy(pw, body)
	if ctx.Err() != nil {
		return // cancelled by a stop or a newer play; expected, not an error
	}
	if err == nil {
		// A live stream never ends on its own: a clean EOF means the server
		// hung up, and without a report playback would sit silent while the
		// status still says playing.
		p.reportError(ctx, errors.New("stream ended unexpectedly"))
		return
	}
	p.reportError(ctx, stallErr(fmt.Errorf("stream read error: %w", err)))
}

// watchdogReader re-arms the stall watchdog on every read that delivers
// data, so the watchdog only fires when the stream stops delivering
// entirely.
type watchdogReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (w *watchdogReader) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	if n > 0 {
		w.timer.Reset(w.timeout)
	}
	return n, err
}

// TrackUpdates returns a channel carrying now-playing title changes for the
// active stream.
func (p *AudioPlayer) TrackUpdates() <-chan TrackInfo {
	return p.trackChan
}

// reportTrack publishes a track update, replacing any pending one so the
// newest title wins. Updates from cancelled (superseded) sessions are dropped.
func (p *AudioPlayer) reportTrack(ctx context.Context, info TrackInfo) {
	if ctx != nil && ctx.Err() != nil {
		return
	}
	select {
	case <-p.trackChan:
	default:
	}
	select {
	case p.trackChan <- info:
	default:
	}
}

// drainTrackUpdates discards any pending track update, so titles from a
// previous channel never surface on the next one.
func (p *AudioPlayer) drainTrackUpdates() {
	select {
	case <-p.trackChan:
	default:
	}
}

// Errors returns a channel for async stream errors. The channel is buffered
// and reportError drops on a full buffer, so a reader is not guaranteed to see
// every failure: it may miss or coalesce errors from a burst. Treat it as "the
// stream is currently unhealthy" signalling, not a lossless error log.
func (p *AudioPlayer) Errors() <-chan error {
	return p.errChan
}

// runSession owns the session's oto player for its entire lifetime: it fades
// the volume in, holds (applying volume changes) until a stop is requested,
// then fades out and releases resources. Because only this goroutine touches
// s.player after Play, volume changes and teardown never race.
func (p *AudioPlayer) runSession(s *session) {
	if p.fadeIn(s) {
		p.holdSession(s)
	}
	p.fadeOutAndClose(s)
}

// holdSession applies volume changes until a stop is requested.
func (p *AudioPlayer) holdSession(s *session) {
	for {
		select {
		case <-s.stop:
			return
		case v := <-s.volumeCh:
			s.player.SetVolume(v)
		}
	}
}

// fadeIn gradually raises the session volume from 0 to the target volume. It
// returns true if the fade completed, or false if a stop was requested
// partway through.
func (p *AudioPlayer) fadeIn(s *session) bool {
	step := fadeInDuration / fadeSteps
	for i := 1; i <= fadeSteps; i++ {
		select {
		case <-s.stop:
			return false
		case <-time.After(step):
			// Re-read the target each step so fades track live volume changes.
			s.player.SetVolume(p.Volume() * float64(i) / fadeSteps)
		}
	}
	return true
}

// SetVolume sets the target volume, clamped to [0, 1]. It applies to the
// active session (via its goroutine) and to all future sessions.
func (p *AudioPlayer) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	p.mu.Lock()
	p.volume = v
	s := p.current
	p.mu.Unlock()

	if s != nil {
		s.setVolume(v)
	}
}

// Volume returns the current target volume in [0, 1].
func (p *AudioPlayer) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
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

// Stop halts the current audio playback and cancels any Play call that is
// still connecting. The fade-out and teardown run asynchronously, so this
// returns immediately.
func (p *AudioPlayer) Stop() {
	p.mu.Lock()
	p.playGen++
	old := p.current
	p.current = nil
	p.mu.Unlock()

	p.drainTrackUpdates()

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
	// Non-blocking send: if the buffer is full the error is dropped rather than
	// stalling the session goroutine. See Errors for what a reader can rely on.
	select {
	case p.errChan <- err:
	default:
	}
}
