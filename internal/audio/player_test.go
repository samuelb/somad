package audio

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"somatui/internal/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestPlayer returns an AudioPlayer without an oto context. This is enough to
// exercise fetchStream and reportError, which never touch the audio device — the
// full Play() path requires hardware and is not testable in CI.
func newTestPlayer() *AudioPlayer {
	return &AudioPlayer{userAgent: "SomaTUI/test", errChan: make(chan error, 2)}
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

func TestFetchStream_Success(t *testing.T) {
	security.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "SomaTUI/test", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("audio-bytes"))
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), server.URL, pw)

	data, err := drainPipe(pr)
	require.NoError(t, err)
	assert.Equal(t, "audio-bytes", string(data))

	// No error should have been reported on the happy path.
	assert.Empty(t, p.errChan)
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
	security.AllowTestHosts(t)
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
	security.AllowTestHosts(t)
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
