package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestPlayer returns an AudioPlayer without an oto context. This is enough to
// exercise fetchStream and reportError, which never touch the audio device — the
// full Play() path requires hardware and is not testable in CI.
func newTestPlayer() *AudioPlayer {
	return &AudioPlayer{
		userAgent: "soma/test",
		errChan:   make(chan error, 2),
		trackChan: make(chan TrackInfo, 1),
	}
}

func TestErrors_ReturnsChannel(t *testing.T) {
	p := newTestPlayer()
	assert.NotNil(t, p.Errors())
}

func TestReportError_NilError(t *testing.T) {
	p := newTestPlayer()

	p.reportError(context.Background(), nil)

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

	p.reportError(ctx, errors.New("boom"))

	select {
	case <-p.errChan:
		t.Fatal("error should be suppressed when context is cancelled")
	default:
	}
}

func TestReportError_Delivers(t *testing.T) {
	p := newTestPlayer()

	p.reportError(context.Background(), errors.New("stream failed"))

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
	p.reportError(context.Background(), errors.New("1"))
	p.reportError(context.Background(), errors.New("2"))
	p.reportError(context.Background(), errors.New("3")) // dropped, must not block

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
	go func() { playErr <- p.Play(server.URL) }()

	<-requestArrived
	p.Stop() // supersedes the in-flight Play
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
	go p.fetchStream(context.Background(), server.URL, pw)

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
		p.fetchStream(context.Background(), server.URL, pw)
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
	go p.fetchStream(context.Background(), server.URL, pw)

	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream stalled")

	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream stalled")
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
	go p.fetchStream(context.Background(), server.URL, pw)

	data, err := drainPipe(pr)
	require.NoError(t, err)

	assert.Equal(t, "1", gotIcyHeader, "fetchStream must request ICY metadata")
	assert.Equal(t, bytes.Repeat([]byte{0xAA}, 8), data, "metadata must not reach the decoder")

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "Demuxed Song", info.Title)
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
	go p.fetchStream(context.Background(), server.URL, pw)

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

	go p.fetchStream(context.Background(), "http://evil.example.com/stream", pw)

	// The pipe reader should observe the error propagated via CloseWithError.
	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stream URL")

	// And the same class of error should be reported on the errors channel.
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "invalid stream URL")
	default:
		t.Fatal("expected an error to be reported")
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
	go p.fetchStream(context.Background(), server.URL, pw)

	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")

	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "500")
	default:
		t.Fatal("expected a status-code error to be reported")
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
		p.fetchStream(ctx, server.URL, pw)
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
