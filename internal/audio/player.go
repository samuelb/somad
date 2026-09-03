package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"somad/internal/security"

	"github.com/ebitengine/oto/v3"
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

// streamConnectTimeout bounds the connect phase of a stream fetch: from the
// request until the first body byte. It is deliberately shorter than the
// stall watchdog, which is tuned for an established stream: a server that
// does not even answer should fail fast so the next candidate (or the
// reconnect backoff) gets its turn and the client's play call does not
// time out first. A variable so tests can shrink it.
var streamConnectTimeout = 10 * time.Second

// Stream buffering (see streamBuffer): at SomaFM's usual 128 kbps the
// capacity holds about half a minute of audio and the prefill about two
// seconds — a head start that rides out ordinary delivery jitter. Icecast
// bursts an initial chunk on connect, so the prefill normally fills
// instantly; the deadline bounds the wait on servers that trickle instead.
const (
	streamBufferSize    = 512 << 10
	streamBufferPrefill = 32 << 10
)

// streamBufferPrefillWait is the prefill deadline. A variable so tests can
// shrink it.
var streamBufferPrefillWait = time.Second

// ErrSuperseded is returned by Play when a newer Play or Stop request arrived
// while this one was still connecting; the newer request owns the audio state.
var ErrSuperseded = errors.New("playback superseded by a newer request")

// Player is the interface for audio playback operations.
// This allows mocking the player in tests.
//
// Play and Stop carry the caller's generation number: a monotonically
// increasing counter the caller bumps for every play or stop request. The
// player commits a session only while its generation is the newest it has
// seen, so a request that was issued earlier but reaches the player later
// (a slow playlist resolve racing a stop, say) can never start stale audio.
// Errors and track updates are stamped with the generation of the session
// that produced them, so the caller can drop reports from a session it has
// already replaced.
type Player interface {
	// Play streams the URL, decoding it as the given format (one of the
	// formats listed by PreferredFormats). It returns ErrSuperseded when gen
	// is older than a generation the player has already seen.
	Play(url, format string, gen uint64) error
	// Stop halts playback. A gen older than the newest seen is ignored so a
	// stale stop cannot tear down a newer session; a gen equal to it stops
	// that session (the caller reacting to its stream error).
	Stop(gen uint64)
	Errors() <-chan error
	TrackUpdates() <-chan TrackInfo
	SetVolume(v float64)
	Volume() float64
}

// StreamError is an asynchronous failure of a committed session, carrying
// the generation that session was started with.
type StreamError struct {
	Gen uint64
	Err error
}

func (e *StreamError) Error() string { return e.Err.Error() }

func (e *StreamError) Unwrap() error { return e.Err }

// outputPlayer and audioContext are the parts of oto used by AudioPlayer.
// Keeping this boundary small lets the device lifecycle be tested without
// requiring audio hardware.
type outputPlayer interface {
	Play()
	Pause()
	SetVolume(float64)
	Volume() float64
}

type audioContext interface {
	NewPlayer(io.Reader) outputPlayer
	Suspend() error
	Resume() error
	Err() error
}

type otoContext struct {
	*oto.Context
}

func (c *otoContext) NewPlayer(r io.Reader) outputPlayer {
	return c.Context.NewPlayer(r)
}

// session represents a single playback lifecycle: one stream, one decoder,
// one oto player. After creation, only its managing goroutine (runSession)
// touches the oto player, which keeps volume changes free of data races.
type session struct {
	player   outputPlayer
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
	userAgent string
	errChan   chan error
	trackChan chan TrackInfo

	contextOnce     sync.Once
	contextErr      error // context creation errors are permanent in oto
	ctx             audioContext
	contextReady    <-chan struct{}
	lateSuspendOnce sync.Once
	newContext      func() (audioContext, <-chan struct{}, error)
	// deviceMu must be acquired before mu when both are needed.
	deviceMu sync.Mutex // guards deviceSuspended and context/session transitions
	// deviceSuspended is valid after ctx is initialized and guarded by deviceMu.
	deviceSuspended bool

	mu       sync.Mutex
	current  *session // the active session, guarded by mu
	sessions int      // committed sessions still fading or playing, guarded by mu
	playGen  uint64   // newest generation seen from Play/Stop; stale ones never commit
	volume   float64  // target volume in [0, 1], guarded by mu
}

// audioReadyTimeout bounds how long the first Play waits for the audio device.
// Without it, a hung audio backend (a stuck ALSA daemon, a broken device)
// would block playback forever instead of failing with a message.
var audioReadyTimeout = 15 * time.Second

// NewPlayer initializes an audio player without opening the audio device. The
// process-global oto context is created lazily by the first Play call.
func NewPlayer(userAgent string) (*AudioPlayer, error) {
	return &AudioPlayer{
		userAgent: userAgent,
		errChan:   make(chan error, 2),
		trackChan: make(chan TrackInfo, 1),
		volume:    1,
		newContext: func() (audioContext, <-chan struct{}, error) {
			op := &oto.NewContextOptions{
				SampleRate:   sampleRate,
				ChannelCount: 2,
				Format:       oto.FormatSignedInt16LE,
			}
			ctx, ready, err := oto.NewContext(op)
			if err != nil {
				return nil, nil, err
			}
			return &otoContext{Context: ctx}, ready, nil
		},
	}, nil
}

// ensureContext creates the process-global oto context once and waits until the
// device is ready. A readiness timeout is not sticky: oto initialization can
// finish later, and a subsequent Play can then use the recovered device.
func (p *AudioPlayer) ensureContext() error {
	p.contextOnce.Do(func() {
		ctx, ready, err := p.newContext()
		if err != nil {
			p.contextErr = fmt.Errorf("failed to create oto context: %w", err)
			return
		}
		p.ctx = ctx
		p.contextReady = ready
	})
	if p.contextErr != nil {
		return p.contextErr
	}

	select {
	case <-p.contextReady:
	case <-time.After(audioReadyTimeout):
		// NewContext has no cancellation or Close operation. If it becomes
		// ready later, stop its render loop unless another Play committed first.
		p.lateSuspendOnce.Do(func() {
			go func() {
				<-p.contextReady
				p.deviceMu.Lock()
				p.mu.Lock()
				p.suspendIfIdleLocked()
				p.mu.Unlock()
				p.deviceMu.Unlock()
			}()
		})
		return fmt.Errorf("audio device not ready after %s", audioReadyTimeout)
	}
	if err := p.ctx.Err(); err != nil {
		return fmt.Errorf("failed to initialize audio device: %w", err)
	}
	return nil
}

// Play starts streaming and playing audio from the given URL, decoded as
// format. It blocks until the stream is decoding and playback has begun; the
// previous session (if any) fades out and tears down asynchronously. Play is
// safe to call concurrently: if another Play or Stop with a newer generation
// arrives while this one is still connecting, the newer request wins and
// this one returns ErrSuperseded without touching the audio state. The same
// generation may be retried (the caller falling back to another stream
// candidate) but never an older one.
func (p *AudioPlayer) Play(url, format string, gen uint64) error {
	p.mu.Lock()
	if gen < p.playGen {
		p.mu.Unlock()
		return ErrSuperseded
	}
	p.playGen = gen
	p.mu.Unlock()

	// Create a pipe to connect the HTTP stream to the MP3 decoder.
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())

	discard := func() {
		cancel()
		_ = pr.Close()
		_ = pw.Close()
	}

	go p.fetchStream(ctx, gen, url, pw)

	// Decode the stream from the pipe reader. This is the only synchronous
	// failure mode, so the new session is not committed until decoding succeeds.
	decoder, err := newDecoder(format, pr)
	if err != nil {
		discard()
		return fmt.Errorf("failed to decode %s stream: %w", format, err)
	}

	// The oto context runs at a fixed rate; resample if the stream differs.
	var decodedStream io.Reader = decoder
	if decoder.SampleRate() != sampleRate {
		decodedStream = newResampler(decoder, decoder.SampleRate(), sampleRate)
	}
	// oto pulls from this reader on its own goroutine and, on error, stops
	// pulling and parks the error in Player.Err, which nothing polls. Without
	// this wrapper a decode failure after Play returned would leave playback
	// silent under a "playing" status (the stall watchdog cannot help: the
	// network side blocks on the full jitter buffer, not on the socket). The
	// session's ctx scopes the report so a session that is already fading
	// out cannot kill its successor.
	decodedStream = &errorReportingReader{r: decodedStream, report: func(err error) {
		if errors.Is(err, io.EOF) {
			// A live stream never ends; the network side normally reports
			// this first, and the duplicate is ignored once the server has
			// left the playing state.
			err = errors.New("decoder reached end of stream")
		}
		p.reportError(ctx, gen, fmt.Errorf("decode error: %w", err))
	}}
	p.mu.Lock()
	superseded := gen != p.playGen
	p.mu.Unlock()
	if superseded {
		discard()
		return ErrSuperseded
	}
	if err := p.ensureContext(); err != nil {
		discard()
		return err
	}

	// Commit the new session and stop the old one (which fades out on its own
	// goroutine, briefly crossfading with the new stream for gapless switching).
	// If a newer Play/Stop arrived while we were connecting, back out instead.
	p.deviceMu.Lock()
	p.mu.Lock()
	if gen != p.playGen {
		p.suspendIfIdleLocked()
		p.mu.Unlock()
		p.deviceMu.Unlock()
		discard()
		return ErrSuperseded
	}
	if p.deviceSuspended {
		if err := p.ctx.Resume(); err != nil {
			p.mu.Unlock()
			p.deviceMu.Unlock()
			discard()
			return fmt.Errorf("failed to resume audio device: %w", err)
		}
		p.deviceSuspended = false
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
	p.sessions++
	p.mu.Unlock()
	p.deviceMu.Unlock()

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
//
// Each failure has exactly one owner: before any body bytes flow (request
// setup, connect, status check) the error travels through the pipe alone —
// Play is still blocked in the decoder and returns it synchronously, and
// reporting it here too would leave a stale error queued that could kill a
// later, healthy session. Once the stream is established, errors are
// reported asynchronously via the errors channel.
func (p *AudioPlayer) fetchStream(ctx context.Context, gen uint64, url string, pw *io.PipeWriter) {
	defer func() { _ = pw.Close() }()

	// The watchdog aborts the request when the connection goes silent for
	// streamStallTimeout; reads on the body below re-arm it. It runs from
	// before the request so a server that never answers is caught too.
	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	var stalled, connectTimedOut atomic.Bool
	watchdog := time.AfterFunc(streamStallTimeout, func() {
		stalled.Store(true)
		cancelReq()
	})
	defer watchdog.Stop()
	// The connect deadline runs until the first body byte, where the
	// watchdogReader below disarms it; from then on only the watchdog
	// applies.
	connectTimer := time.AfterFunc(streamConnectTimeout, func() {
		connectTimedOut.Store(true)
		cancelReq()
	})
	defer connectTimer.Stop()
	// stallErr rewrites an error caused by one of the timers' own
	// cancellation into one that names the timeout.
	stallErr := func(err error) error {
		if connectTimedOut.Load() {
			return fmt.Errorf("stream connect timed out: no data received within %s", streamConnectTimeout)
		}
		if stalled.Load() {
			return fmt.Errorf("stream stalled: no data received for %s", streamStallTimeout)
		}
		return err
	}

	req, err := security.NewRequest(reqCtx, url, p.userAgent)
	if err != nil {
		pw.CloseWithError(fmt.Errorf("invalid stream URL: %w", err))
		return
	}
	req.Header.Set("Icy-MetaData", "1") // Request interleaved ICY metadata

	resp, err := security.HTTPClient.Do(req) // #nosec G704 -- URL validated by security.NewRequest()
	if err != nil {
		pw.CloseWithError(stallErr(fmt.Errorf("failed to fetch stream: %w", err)))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		pw.CloseWithError(fmt.Errorf("unexpected status code: %d", resp.StatusCode))
		return
	}

	// Buffer between the network and the decoder so delivery jitter is
	// absorbed here instead of reaching playback. The buffer's fill error
	// (EOF included) surfaces on the read side after the buffered bytes
	// drain, so the error handling below is unchanged. When fetchStream
	// returns, the deferred cancelReq unblocks the fill goroutine's network
	// read; Close alone could not.
	raw := &watchdogReader{r: resp.Body, timer: watchdog, timeout: streamStallTimeout, connect: connectTimer}
	buf := newStreamBuffer(raw, streamBufferSize, streamBufferPrefill, streamBufferPrefillWait)
	defer buf.Close()

	// If the server honored the metadata request, demux titles out of the
	// stream; otherwise the body is pure audio and passes through untouched.
	// The demuxer consumes the buffer's output, so titles surface roughly
	// when their audio is decoded, not when the network delivered them.
	var body io.Reader = buf
	if icyInt, err := strconv.Atoi(resp.Header.Get("icy-metaint")); err == nil && icyInt > 0 {
		body = newICYDemuxer(body, icyInt, func(title string) {
			p.reportTrack(ctx, TrackInfo{Title: title, Gen: gen})
		})
	}

	// Copy the stream to the pipe writer until cancelled or the stream ends.
	n, err := io.Copy(pw, body)
	if ctx.Err() != nil {
		return // cancelled by a stop or a newer play; expected, not an error
	}
	if n == 0 && err != nil {
		// Headers arrived but no audio ever did (the connect deadline, or a
		// read error on the first chunk): Play is still parked in the
		// decoder, so the pipe is the failure's only owner, exactly as for
		// a failure before the response.
		pw.CloseWithError(stallErr(fmt.Errorf("stream read error: %w", err)))
		return
	}
	if err == nil {
		// A live stream never ends on its own: a clean EOF means the server
		// hung up, and without a report playback would sit silent while the
		// status still says playing.
		p.reportError(ctx, gen, errors.New("stream ended unexpectedly"))
		return
	}
	p.reportError(ctx, gen, stallErr(fmt.Errorf("stream read error: %w", err)))
}

// errorReportingReader forwards reads and hands the first error (EOF
// included) to report, once. It sits between the decoder and the oto player
// so decode failures after Play has returned become visible.
type errorReportingReader struct {
	r      io.Reader
	once   sync.Once
	report func(error)
}

func (e *errorReportingReader) Read(b []byte) (int, error) {
	n, err := e.r.Read(b)
	if err != nil {
		e.once.Do(func() { e.report(err) })
	}
	return n, err
}

// watchdogReader re-arms the stall watchdog on every read that delivers
// data, so the watchdog only fires when the stream stops delivering
// entirely. The first byte also disarms the connect deadline, when one is
// set.
type watchdogReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
	connect *time.Timer // may be nil
}

func (w *watchdogReader) Read(b []byte) (int, error) {
	n, err := w.r.Read(b)
	if n > 0 {
		if w.connect != nil {
			w.connect.Stop()
			w.connect = nil
		}
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
			s.player.SetVolume(perceptualVolume(p.Volume() * float64(i) / fadeSteps))
		}
	}
	return true
}

// perceptualVolume maps a linear volume target in [0, 1] to the amplitude
// handed to the oto player. Loudness perception is roughly the cube of
// amplitude (a Stevens'-power-law approximation good enough for a radio
// player), so a linear target concentrates most of the audible change in the
// bottom quarter of the range; cubing spreads it evenly. This is the single
// place that mapping happens — SetVolume, and the fade-in/fade-out steps
// that scale the same target linearly, all route through it — so Volume()
// keeps returning the plain, un-curved percent the wire protocol uses.
func perceptualVolume(target float64) float64 {
	return target * target * target
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
		s.setVolume(perceptualVolume(v))
	}
}

// Volume returns the current target volume in [0, 1], un-curved.
func (p *AudioPlayer) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

// fadeOutAndClose gradually lowers the session volume to 0, then pauses the
// player, closes the stream, and cancels the HTTP fetch.
func (p *AudioPlayer) fadeOutAndClose(s *session) {
	step := fadeOutDuration / fadeSteps
	// Start from the amplitude the player is actually at (a stop can land
	// mid fade-in), mapped back to the linear target so the fade steps run
	// through the same curve as everything else.
	startTarget := math.Cbrt(s.player.Volume())
	for i := fadeSteps - 1; i >= 0; i-- {
		s.player.SetVolume(perceptualVolume(startTarget * float64(i) / fadeSteps))
		time.Sleep(step)
	}
	s.player.Pause()
	// Cancel before closing the pipe: with the context already cancelled,
	// fetchStream suppresses the resulting pipe/read error instead of
	// reporting a spurious "stream read error" (and triggering an unwanted
	// reconnect) on a clean stop. Closing second still unblocks a writer
	// stuck in a pipe write.
	s.cancel()
	_ = s.stream.Close()

	p.deviceMu.Lock()
	p.mu.Lock()
	p.sessions--
	p.suspendIfIdleLocked()
	p.mu.Unlock()
	p.deviceMu.Unlock()
}

// suspendIfIdleLocked stops the device render loop when no session is active.
// Both deviceMu and mu must be held so a concurrent Play cannot resume and
// commit a new session between the idle check and Suspend.
func (p *AudioPlayer) suspendIfIdleLocked() {
	if p.current == nil && p.sessions == 0 && p.ctx != nil && !p.deviceSuspended {
		if err := p.ctx.Suspend(); err == nil {
			p.deviceSuspended = true
		}
	}
}

// Stop halts the current audio playback and cancels any Play call that is
// still connecting, unless gen is older than the newest generation seen (a
// stale stop must not tear down a newer session). The fade-out and teardown
// run asynchronously, so this returns immediately.
func (p *AudioPlayer) Stop(gen uint64) {
	p.mu.Lock()
	if gen < p.playGen {
		p.mu.Unlock()
		return
	}
	p.playGen = gen
	old := p.current
	p.current = nil
	p.mu.Unlock()

	p.drainTrackUpdates()

	if old != nil {
		old.requestStop()
	}
}

// reportError publishes an asynchronous failure of the session started with
// gen. Errors from cancelled (superseded) sessions are dropped.
func (p *AudioPlayer) reportError(ctx context.Context, gen uint64, err error) {
	if err == nil {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	// Non-blocking send: if the buffer is full the error is dropped rather than
	// stalling the session goroutine. See Errors for what a reader can rely on.
	select {
	case p.errChan <- &StreamError{Gen: gen, Err: err}:
	default:
	}
}
