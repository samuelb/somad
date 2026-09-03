package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testGen hands out increasing generation numbers for Play and Stop, the
// way the server's counter does.
var testGen atomic.Uint64

// playNext calls Play with the next generation.
func playNext(p *AudioPlayer, url, format string) error {
	return p.Play(url, format, testGen.Add(1))
}

// stopNext calls Stop with the next generation.
func stopNext(p *AudioPlayer) {
	p.Stop(testGen.Add(1))
}

// newTestPlayer returns a bare AudioPlayer without an oto context. This is
// enough for tests that exercise methods which never touch the audio device.
func newTestPlayer() *AudioPlayer {
	return &AudioPlayer{
		userAgent: "soma/test",
		errChan:   make(chan error, 2),
		trackChan: make(chan TrackInfo, 1),
	}
}

type fakeOutputPlayer struct {
	mu     sync.Mutex
	volume float64
	paused func()
}

func (p *fakeOutputPlayer) Play() {}

func (p *fakeOutputPlayer) Pause() {
	if p.paused != nil {
		p.paused()
	}
}

func (p *fakeOutputPlayer) SetVolume(v float64) {
	p.mu.Lock()
	p.volume = v
	p.mu.Unlock()
}

func (p *fakeOutputPlayer) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

type fakeAudioContext struct {
	suspends atomic.Int32
	resumes  atomic.Int32
	players  atomic.Int32
	pauses   atomic.Int32
	// pump makes each player consume its reader on a goroutine until the
	// first read error, like oto's render loop does.
	pump bool

	mu        sync.Mutex
	resumeErr error
}

func (c *fakeAudioContext) NewPlayer(r io.Reader) outputPlayer {
	c.players.Add(1)
	if c.pump {
		go func() {
			buf := make([]byte, 4096)
			for {
				if _, err := r.Read(buf); err != nil {
					return
				}
			}
		}()
	}
	return &fakeOutputPlayer{
		volume: 1,
		paused: func() { c.pauses.Add(1) },
	}
}

func (c *fakeAudioContext) Suspend() error {
	c.suspends.Add(1)
	return nil
}

func (c *fakeAudioContext) Resume() error {
	c.resumes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resumeErr
}

func (c *fakeAudioContext) Err() error { return nil }

func (c *fakeAudioContext) setResumeError(err error) {
	c.mu.Lock()
	c.resumeErr = err
	c.mu.Unlock()
}

func newLifecycleTestPlayer(t *testing.T) (*AudioPlayer, *fakeAudioContext, *atomic.Int32) {
	t.Helper()
	// The streaming test server sends less than the prefill and then holds
	// the connection, so without this every Play would wait out the full
	// prefill deadline.
	origWait := streamBufferPrefillWait
	streamBufferPrefillWait = 10 * time.Millisecond
	t.Cleanup(func() { streamBufferPrefillWait = origWait })
	p, err := NewPlayer("soma/test")
	require.NoError(t, err)
	ctx := &fakeAudioContext{}
	created := &atomic.Int32{}
	ready := make(chan struct{})
	close(ready)
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		created.Add(1)
		return ctx, ready, nil
	}
	return p, ctx, created
}

func newStreamingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(silentMP3Frames(30))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

func TestNewPlayer_DoesNotOpenAudioDevice(t *testing.T) {
	_, _, created := newLifecycleTestPlayer(t)
	assert.Zero(t, created.Load())
}

func TestPlayStop_ResumesAndSuspendsAudioDevice(t *testing.T) {
	p, ctx, created := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	assert.EqualValues(t, 1, created.Load())
	assert.EqualValues(t, 1, ctx.players.Load())
	assert.Zero(t, ctx.resumes.Load(), "a new context is already active")
	assert.Zero(t, ctx.suspends.Load())

	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	assert.EqualValues(t, 1, ctx.resumes.Load())
	assert.EqualValues(t, 2, ctx.players.Load())
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 2
	}, time.Second, 10*time.Millisecond)
}

func TestEnsureContext_RecoversAfterReadyTimeout(t *testing.T) {
	p, err := NewPlayer("soma/test")
	require.NoError(t, err)
	ctx := &fakeAudioContext{}
	ready := make(chan struct{})
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		return ctx, ready, nil
	}
	originalTimeout := audioReadyTimeout
	audioReadyTimeout = 20 * time.Millisecond
	t.Cleanup(func() { audioReadyTimeout = originalTimeout })

	err = p.ensureContext()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audio device not ready")

	close(ready)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, p.ensureContext())
}

func TestPlay_ResumeErrorDoesNotCommitSession(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)

	ctx.setResumeError(errors.New("resume failed"))
	err := playNext(p, server.URL, FormatMP3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resume audio device")
	assert.EqualValues(t, 1, ctx.players.Load())
	p.mu.Lock()
	assert.Nil(t, p.current)
	assert.Zero(t, p.sessions)
	p.mu.Unlock()

	ctx.setResumeError(nil)
	require.NoError(t, playNext(p, server.URL, FormatMP3))
}

func TestPlaySwitch_DoesNotSuspendReplacementSession(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.Eventually(t, func() bool {
		return ctx.pauses.Load() == 1
	}, time.Second, 10*time.Millisecond)

	// The old session has completed teardown. It must see the new current
	// session and leave the context running.
	assert.Zero(t, ctx.suspends.Load())
	assert.Zero(t, ctx.resumes.Load())

	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.pauses.Load() == 2 && ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestStopDuringCrossfade_WaitsForBothSessionsBeforeSuspend(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)

	require.Eventually(t, func() bool {
		return ctx.pauses.Load() >= 1
	}, time.Second, 10*time.Millisecond)
	if ctx.pauses.Load() == 1 {
		assert.Zero(t, ctx.suspends.Load(), "one session is still draining")
	}
	require.Eventually(t, func() bool {
		return ctx.pauses.Load() == 2 && ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestErrors_ReturnsChannel(t *testing.T) {
	p := newTestPlayer()
	assert.NotNil(t, p.Errors())
}

func TestReportError_NilError(t *testing.T) {
	p := newTestPlayer()

	p.reportError(context.Background(), 1, nil)

	select {
	case <-p.errChan:
		t.Fatal("nil error should not be sent")
	default:
	}
}

func TestReportError_CancelledContext(t *testing.T) {
	p := newTestPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p.reportError(ctx, 1, errors.New("boom"))

	select {
	case <-p.errChan:
		t.Fatal("error should be suppressed when context is cancelled")
	default:
	}
}

func TestReportError_Delivers(t *testing.T) {
	p := newTestPlayer()

	p.reportError(context.Background(), 1, errors.New("stream failed"))

	select {
	case err := <-p.errChan:
		assert.EqualError(t, err, "stream failed")
	default:
		t.Fatal("expected error to be delivered")
	}
}

func TestReportError_FullChannelDoesNotBlock(t *testing.T) {
	p := newTestPlayer()

	// Fill the buffered channel (capacity 2), then a third report must not block.
	p.reportError(context.Background(), 1, errors.New("1"))
	p.reportError(context.Background(), 1, errors.New("2"))
	p.reportError(context.Background(), 1, errors.New("3")) // dropped, must not block

	assert.Len(t, p.errChan, 2)
}

// drainPipe reads everything from r until EOF or error, returning the bytes read
// and the terminating error.
func drainPipe(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// silentMP3Frames returns n silent MPEG-1 Layer III frames (44.1 kHz, 128 kbps,
// stereo): a sync header followed by all-zero side info and main data.
func silentMP3Frames(n int) []byte {
	const frameSize = 417 // 144 * 128000 / 44100
	frame := make([]byte, frameSize)
	frame[0], frame[1], frame[2], frame[3] = 0xFF, 0xFB, 0x90, 0x64
	buf := make([]byte, 0, n*frameSize)
	for i := 0; i < n; i++ {
		buf = append(buf, frame...)
	}
	return buf
}

func TestPlay_SupersededByStop(t *testing.T) {
	securitytest.AllowTestHosts(t)

	// Hold the stream response until the test has issued Stop, so the Play
	// call is still connecting when it is superseded.
	requestArrived := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestArrived)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write(silentMP3Frames(30))
	}))
	defer server.Close()

	// No oto context: the superseded path must return before touching it.
	p := newTestPlayer()

	playErr := make(chan error, 1)
	go func() { playErr <- playNext(p, server.URL, FormatMP3) }()

	<-requestArrived
	stopNext(p) // supersedes the in-flight Play
	close(release)

	err := <-playErr
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSuperseded)
}

func TestFetchStream_Success(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "soma/test", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("audio-bytes"))
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	data, err := drainPipe(pr)
	require.NoError(t, err)
	assert.Equal(t, "audio-bytes", string(data))

	// A live stream ending (clean EOF) is abnormal and must be reported so
	// the reconnect machinery kicks in instead of playing silence.
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream ended unexpectedly")
	default:
		t.Fatal("expected the stream end to be reported")
	}
}

// shortStallTimeout shrinks the stall watchdog for the duration of a test.
func shortStallTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := streamStallTimeout
	streamStallTimeout = d
	t.Cleanup(func() { streamStallTimeout = orig })
}

func TestFetchStream_StalledStreamReportsError(t *testing.T) {
	securitytest.AllowTestHosts(t)
	shortStallTimeout(t, 150*time.Millisecond)

	// Send some data, then hold the connection open without closing it: the
	// classic silent stall (lost link, NAT timeout) that never errors.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some-audio"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release) // must run before server.Close, which waits on handlers

	p := newTestPlayer()
	pr, pw := io.Pipe()

	done := make(chan struct{})
	go func() {
		p.fetchStream(context.Background(), 1, server.URL, pw)
		close(done)
	}()

	data, _ := drainPipe(pr)
	<-done

	assert.Equal(t, "some-audio", string(data), "data before the stall must pass through")
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream stalled")
	default:
		t.Fatal("expected the stall to be reported")
	}
}

func TestFetchStream_UnresponsiveServerReportsStall(t *testing.T) {
	securitytest.AllowTestHosts(t)
	shortStallTimeout(t, 150*time.Millisecond)

	// The server never even sends response headers.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release) // must run before server.Close, which waits on handlers

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream stalled")

	// A stall before any data flowed is a connect failure: the pipe (and so
	// Play's return value) is its only owner, with no async duplicate.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

// shortConnectTimeout shrinks the stream connect deadline for a test.
func shortConnectTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	orig := streamConnectTimeout
	streamConnectTimeout = d
	t.Cleanup(func() { streamConnectTimeout = orig })
}

func TestFetchStream_ConnectDeadlineFiresBeforeStallWatchdog(t *testing.T) {
	securitytest.AllowTestHosts(t)
	shortStallTimeout(t, 5*time.Second)
	shortConnectTimeout(t, 100*time.Millisecond)

	// Headers arrive but no body ever does: the connect deadline, not the
	// (much longer) stall watchdog, must end the attempt.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release)

	p := newTestPlayer()
	pr, pw := io.Pipe()
	start := time.Now()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	// Headers already arrived, so the failure is owned by the async path.
	data, _ := drainPipe(pr)
	assert.Empty(t, data)
	assert.Less(t, time.Since(start), 2*time.Second)
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream connect timed out")
	default:
		t.Fatal("expected the connect timeout to be reported")
	}
}

func TestFetchStream_ConnectDeadlineDisarmedByFirstByte(t *testing.T) {
	securitytest.AllowTestHosts(t)
	shortStallTimeout(t, 400*time.Millisecond)
	shortConnectTimeout(t, 100*time.Millisecond)

	// Data flows right away, then the stream goes silent for longer than
	// the connect deadline but shorter than the stall watchdog: the
	// connect deadline must be gone by then.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("second"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release)

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	data, _ := drainPipe(pr)
	assert.Equal(t, "firstsecond", string(data))
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream stalled", "the eventual failure is the stall, not the connect deadline")
	default:
		t.Fatal("expected the stall to be reported")
	}
}

func TestWatchdogReader_RearmsOnData(t *testing.T) {
	shortStallTimeout(t, 100*time.Millisecond)

	var fired atomic.Bool
	timer := time.AfterFunc(streamStallTimeout, func() { fired.Store(true) })
	defer timer.Stop()

	pr, pw := io.Pipe()
	w := &watchdogReader{r: pr, timer: timer, timeout: streamStallTimeout}

	// Keep data flowing for well past the stall timeout; the watchdog must
	// not fire while reads succeed.
	go func() {
		for i := 0; i < 6; i++ {
			_, _ = pw.Write([]byte("x"))
			time.Sleep(40 * time.Millisecond)
		}
		_ = pw.Close()
	}()

	_, err := io.ReadAll(w)
	require.NoError(t, err)
	assert.False(t, fired.Load(), "watchdog must not fire while data flows")
}

func TestFetchStream_RequestsAndDemuxesICYMetadata(t *testing.T) {
	securitytest.AllowTestHosts(t)

	var gotIcyHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIcyHeader = r.Header.Get("Icy-MetaData")
		b := &icyStreamBuilder{icyInt: 8}
		b.segment(0xAA, "StreamTitle='Demuxed Song';")
		w.Header().Set("icy-metaint", "8")
		_, _ = w.Write(b.buf.Bytes())
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	data, err := drainPipe(pr)
	require.NoError(t, err)

	assert.Equal(t, "1", gotIcyHeader, "fetchStream must request ICY metadata")
	assert.Equal(t, bytes.Repeat([]byte{0xAA}, 8), data, "metadata must not reach the decoder")

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "Demuxed Song", info.Title)
		assert.EqualValues(t, 1, info.Gen, "titles carry the session generation")
	default:
		t.Fatal("expected a track update from the demuxed metadata")
	}
}

func TestFetchStream_NoICYHeaderPassesThrough(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No icy-metaint header: the body must be forwarded untouched.
		_, _ = w.Write([]byte("plain-audio"))
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	data, err := drainPipe(pr)
	require.NoError(t, err)
	assert.Equal(t, "plain-audio", string(data))
	assert.Empty(t, p.trackChan)
}

func TestSetVolume_ClampsAndStores(t *testing.T) {
	p := newTestPlayer()
	p.volume = 1

	p.SetVolume(0.5)
	assert.InDelta(t, 0.5, p.Volume(), 1e-9)

	p.SetVolume(-0.2)
	assert.Zero(t, p.Volume())

	p.SetVolume(1.7)
	assert.InDelta(t, 1.0, p.Volume(), 1e-9)
}

func TestSessionSetVolume_NewestWins(t *testing.T) {
	s := &session{volumeCh: make(chan float64, 1)}

	s.setVolume(0.3)
	s.setVolume(0.7) // replaces the pending 0.3

	select {
	case v := <-s.volumeCh:
		assert.InDelta(t, 0.7, v, 1e-9)
	default:
		t.Fatal("expected a pending volume target")
	}
}

func TestReportTrack_NewestWins(t *testing.T) {
	p := newTestPlayer()

	p.reportTrack(context.Background(), TrackInfo{Title: "First"})
	p.reportTrack(context.Background(), TrackInfo{Title: "Second"})

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "Second", info.Title)
	default:
		t.Fatal("expected a pending track update")
	}
}

func TestReportTrack_CancelledContextDropped(t *testing.T) {
	p := newTestPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p.reportTrack(ctx, TrackInfo{Title: "Stale"})

	assert.Empty(t, p.trackChan, "superseded sessions must not publish titles")
}

func TestFetchStream_InvalidURL(t *testing.T) {
	p := newTestPlayer()
	pr, pw := io.Pipe()

	go p.fetchStream(context.Background(), 1, "http://evil.example.com/stream", pw)

	// The pipe reader should observe the error propagated via CloseWithError.
	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stream URL")

	// The pipe is the failure's only owner: Play returns it synchronously,
	// and an async duplicate could kill a later, healthy session.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

func TestFetchStream_BadStatusCode(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw)

	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")

	// Synchronous failures own their error exclusively; see above.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

func TestFetchStream_CancelledContextSuppressesReadError(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// Server that blocks so the copy is interrupted by cancellation.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // hold the connection open until the test releases it
	}))
	defer server.Close()
	defer close(release)

	p := newTestPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()

	done := make(chan struct{})
	go func() {
		p.fetchStream(ctx, 1, server.URL, pw)
		close(done)
	}()

	// Cancel the request, then drain the reader so fetchStream can return.
	cancel()
	_, _ = drainPipe(pr)
	<-done

	// A read error caused by our own cancellation must not be reported.
	select {
	case err := <-p.errChan:
		t.Fatalf("cancellation should not report an error, got: %v", err)
	default:
	}
}

// failingDecoder yields silence for a while, then fails like a decoder that
// hit corrupt data mid-stream.
type failingDecoder struct {
	reads int
	err   error
}

func (d *failingDecoder) Read(b []byte) (int, error) {
	d.reads++
	if d.reads > 3 {
		return 0, d.err
	}
	return len(b), nil
}

func (d *failingDecoder) SampleRate() int { return sampleRate }

func TestPlay_DecoderErrorAfterPlayIsReported(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	ctx.pump = true
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	prevDecoder := newDecoder
	newDecoder = func(_ string, r io.Reader) (pcmDecoder, error) {
		// Drain the pipe on a goroutine so the fetch side never blocks; the
		// decoder's own output does not depend on it.
		go func() { _, _ = io.Copy(io.Discard, r) }()
		return &failingDecoder{err: errors.New("corrupt frame")}, nil
	}
	t.Cleanup(func() { newDecoder = prevDecoder })

	require.NoError(t, playNext(p, server.URL, FormatMP3))

	select {
	case err := <-p.Errors():
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode error")
		assert.Contains(t, err.Error(), "corrupt frame")
	case <-time.After(2 * time.Second):
		t.Fatal("decoder error after Play was never reported")
	}
}

func TestPlay_DecoderErrorAfterStopIsSuppressed(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	// Not pumped: the first read happens only after Stop has cancelled the
	// session, which is when a real teardown closes the pipe under the
	// decoder.
	var reader io.Reader
	prevDecoder := newDecoder
	newDecoder = func(_ string, r io.Reader) (pcmDecoder, error) {
		return &failingDecoder{err: errors.New("closed pipe")}, nil
	}
	t.Cleanup(func() { newDecoder = prevDecoder })
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		ready := make(chan struct{})
		close(ready)
		return &captureContext{fakeAudioContext: ctx, onPlayer: func(r io.Reader) { reader = r }}, ready, nil
	}

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)
	require.Eventually(t, func() bool { return ctx.pauses.Load() == 1 }, time.Second, 10*time.Millisecond)

	buf := make([]byte, 16)
	for i := 0; i < 5; i++ {
		_, _ = reader.Read(buf)
	}
	select {
	case err := <-p.Errors():
		t.Fatalf("error from a stopped session must be dropped, got %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

// captureContext hands the reader given to NewPlayer to a callback.
type captureContext struct {
	*fakeAudioContext
	onPlayer func(io.Reader)
}

func (c *captureContext) NewPlayer(r io.Reader) outputPlayer {
	c.onPlayer(r)
	return c.fakeAudioContext.NewPlayer(r)
}

func TestPlay_StaleGenerationIsRefused(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	newer := testGen.Add(2)
	require.NoError(t, p.Play(server.URL, FormatMP3, newer))
	// A request issued before the one that just committed arrives late: it
	// must not touch the audio state.
	require.ErrorIs(t, p.Play(server.URL, FormatMP3, newer-1), ErrSuperseded)
	assert.EqualValues(t, 1, ctx.players.Load())
	// Retrying the same generation (a stream-candidate fallback) is allowed.
	require.NoError(t, p.Play(server.URL, FormatMP3, newer))
	assert.EqualValues(t, 2, ctx.players.Load())
}

func TestStop_StaleGenerationIsIgnored(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	gen := testGen.Add(2)
	require.NoError(t, p.Play(server.URL, FormatMP3, gen))
	p.Stop(gen - 1)
	p.mu.Lock()
	current := p.current
	p.mu.Unlock()
	assert.NotNil(t, current, "a stale stop must not tear down a newer session")
	assert.Zero(t, ctx.pauses.Load())

	// The session's own generation stops it (the server reacting to its
	// stream error).
	p.Stop(gen)
	require.Eventually(t, func() bool { return ctx.pauses.Load() == 1 }, time.Second, 10*time.Millisecond)
}

func TestReportError_CarriesGeneration(t *testing.T) {
	p := newTestPlayer()
	p.reportError(context.Background(), 7, errors.New("boom"))
	err := <-p.errChan
	var se *StreamError
	require.ErrorAs(t, err, &se)
	assert.EqualValues(t, 7, se.Gen)
	assert.EqualError(t, err, "boom")
}
