package audio

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// feedReader is a test source whose data arrives in explicitly released
// steps, with a terminal error after the last one.
type feedReader struct {
	ch  chan []byte
	err error

	pending []byte
}

func newFeedReader(err error) *feedReader {
	return &feedReader{ch: make(chan []byte, 16), err: err}
}

func (f *feedReader) feed(p []byte) { f.ch <- p }
func (f *feedReader) done()         { close(f.ch) }

func (f *feedReader) Read(p []byte) (int, error) {
	if len(f.pending) == 0 {
		chunk, ok := <-f.ch
		if !ok {
			return 0, f.err
		}
		f.pending = chunk
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

func TestStreamBufferPassesDataThrough(t *testing.T) {
	t.Parallel()
	data := make([]byte, 100*1024)
	_, err := rand.Read(data)
	require.NoError(t, err)

	// A capacity far below the payload forces wrap-around and writer
	// backpressure; the bytes must still come out intact and in order.
	b := newStreamBuffer(bytes.NewReader(data), 4*1024, 1024, time.Minute)
	defer b.Close()

	got, err := io.ReadAll(b)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

func TestStreamBufferPrefillHoldsFirstRead(t *testing.T) {
	t.Parallel()
	src := newFeedReader(io.EOF)
	b := newStreamBuffer(src, 64*1024, 1024, time.Minute)
	defer b.Close()

	read := make(chan struct{})
	go func() {
		buf := make([]byte, 16)
		_, _ = b.Read(buf)
		close(read)
	}()

	// Below the prefill threshold the read must stay blocked.
	src.feed(make([]byte, 512))
	select {
	case <-read:
		t.Fatal("read returned before the prefill was satisfied")
	case <-time.After(50 * time.Millisecond):
	}

	// Crossing the threshold releases it.
	src.feed(make([]byte, 512))
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not return after the prefill was satisfied")
	}
}

func TestStreamBufferPrefillDeadlineReleasesRead(t *testing.T) {
	t.Parallel()
	src := newFeedReader(io.EOF)
	b := newStreamBuffer(src, 64*1024, 32*1024, 50*time.Millisecond)
	defer b.Close()

	// Far less than the prefill: only the deadline can release the read.
	src.feed([]byte("abc"))

	buf := make([]byte, 16)
	n, err := b.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "abc", string(buf[:n]))
}

func TestStreamBufferDeliversErrorAfterDrain(t *testing.T) {
	t.Parallel()
	fillErr := errors.New("connection reset")
	src := newFeedReader(fillErr)
	b := newStreamBuffer(src, 64*1024, 4, time.Minute)
	defer b.Close()

	src.feed([]byte("last words"))
	src.done()

	got, err := io.ReadAll(b)
	assert.Equal(t, "last words", string(got))
	assert.ErrorIs(t, err, fillErr)
}

func TestStreamBufferEOFAfterDrain(t *testing.T) {
	t.Parallel()
	b := newStreamBuffer(bytes.NewReader([]byte("tail")), 64*1024, 1, time.Minute)
	defer b.Close()

	got, err := io.ReadAll(b) // ReadAll treats io.EOF as a clean end
	require.NoError(t, err)
	assert.Equal(t, "tail", string(got))
}

func TestStreamBufferCloseUnblocksReader(t *testing.T) {
	t.Parallel()
	src := newFeedReader(io.EOF)
	b := newStreamBuffer(src, 64*1024, 0, time.Minute)

	errCh := make(chan error, 1)
	go func() {
		_, err := b.Read(make([]byte, 16))
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the read block on the empty buffer
	b.Close()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, io.ErrClosedPipe)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the reader")
	}
}

func TestStreamBufferCloseUnblocksWriter(t *testing.T) {
	t.Parallel()
	src := newFeedReader(io.EOF)
	b := newStreamBuffer(src, 8, 0, time.Minute)

	// Overfill the tiny buffer so the fill goroutine blocks on backpressure,
	// then close; write returns false and the fill goroutine exits instead
	// of hanging.
	src.feed(make([]byte, 64))
	time.Sleep(20 * time.Millisecond) // let the writer block on the full buffer
	b.Close()

	// A subsequent read reports the closed buffer rather than hanging.
	_, err := b.Read(make([]byte, 16))
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestStreamBufferZeroPrefillReadsImmediately(t *testing.T) {
	t.Parallel()
	src := newFeedReader(io.EOF)
	b := newStreamBuffer(src, 64*1024, 0, time.Minute)
	defer b.Close()

	src.feed([]byte("now"))
	buf := make([]byte, 16)
	n, err := b.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "now", string(buf[:n]))
}

func TestStreamBufferPrefillCappedAtCapacity(t *testing.T) {
	t.Parallel()
	src := newFeedReader(io.EOF)
	// Prefill above capacity must clamp, or the read below could never wake.
	b := newStreamBuffer(src, 8, 1024, time.Minute)
	defer b.Close()

	src.feed(make([]byte, 8))
	n, err := b.Read(make([]byte, 16))
	require.NoError(t, err)
	assert.Equal(t, 8, n)
}
