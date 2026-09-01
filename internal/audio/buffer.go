package audio

import (
	"io"
	"sync"
	"time"
)

// streamBuffer is the jitter buffer between the network fetch and the
// decoder: a fill goroutine drains the source as fast as the network
// delivers, and the decoder reads at playback rate from the buffered bytes.
// Without it the decoder is fed at exactly the pace of the network (the pipe
// to the decoder is unbuffered), so any delivery hiccup longer than the audio
// device's own small buffer is audible.
//
// The first read waits for a prefill — a modest head start of buffered
// audio — bounded by a deadline so a slow network delays start-up only
// briefly instead of stalling it. Prefill is satisfied once: after an
// underrun, reads resume as soon as any byte arrives rather than waiting for
// a fresh head start, trading a possible stutter for the shortest dropout.
//
// The fill side's terminal error (io.EOF included) is delivered to the
// reader only after the buffered bytes are drained, so a network drop still
// plays out the audio already fetched.
type streamBuffer struct {
	mu      sync.Mutex
	cond    *sync.Cond
	buf     []byte
	start   int // read position
	size    int // bytes currently buffered
	prefill int // bytes that must be buffered before the first read
	// err is the fill side's terminal error, delivered after the buffer
	// drains. primed reports the prefill condition; it is forced true by a
	// fill error, the deadline, or Close, so a reader never waits on a
	// prefill that can no longer complete.
	err    error
	primed bool
	closed bool
}

// newStreamBuffer starts buffering from src. Reads block until prefill bytes
// are buffered or prefillWait has elapsed, whichever comes first. The fill
// goroutine exits when src fails (or ends) or the buffer is closed; a caller
// that closes the buffer must also unblock src (cancel the request), since a
// fill blocked on the network cannot be interrupted from here.
func newStreamBuffer(src io.Reader, capacity, prefill int, prefillWait time.Duration) *streamBuffer {
	b := &streamBuffer{buf: make([]byte, capacity)}
	b.cond = sync.NewCond(&b.mu)
	// A prefill larger than the buffer could never be reached.
	if prefill > capacity {
		prefill = capacity
	}
	b.prefill = prefill
	var timer *time.Timer
	if prefill <= 0 {
		b.primed = true
	} else {
		timer = time.AfterFunc(prefillWait, b.prime)
	}
	go func() {
		b.fill(src)
		if timer != nil {
			timer.Stop()
		}
	}()
	return b
}

// fill drains src into the buffer until src fails or the buffer is closed.
func (b *streamBuffer) fill(src io.Reader) {
	chunk := make([]byte, 32*1024)
	for {
		n, err := src.Read(chunk)
		if n > 0 && !b.write(chunk[:n]) {
			return // closed underneath us
		}
		if err != nil {
			b.fail(err)
			return
		}
	}
}

// write copies p into the buffer, blocking while it is full. It reports
// false when the buffer was closed.
func (b *streamBuffer) write(p []byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(p) > 0 {
		for b.size == len(b.buf) && !b.closed {
			b.cond.Wait()
		}
		if b.closed {
			return false
		}
		// Copy into the free region, capped at the wrap-around point.
		end := (b.start + b.size) % len(b.buf)
		n := len(b.buf) - b.size
		if room := len(b.buf) - end; n > room {
			n = room
		}
		if n > len(p) {
			n = len(p)
		}
		copy(b.buf[end:end+n], p[:n])
		b.size += n
		p = p[n:]
		if !b.primed && b.size >= b.prefill {
			b.primed = true
		}
		b.cond.Broadcast()
	}
	return true
}

// prime releases readers waiting on the prefill.
func (b *streamBuffer) prime() {
	b.mu.Lock()
	b.primed = true
	b.mu.Unlock()
	b.cond.Broadcast()
}

// fail records the fill side's terminal error and releases all waiters.
func (b *streamBuffer) fail(err error) {
	b.mu.Lock()
	b.err = err
	b.primed = true
	b.mu.Unlock()
	b.cond.Broadcast()
}

// Read returns buffered bytes, blocking until the prefill is satisfied and
// data (or the fill side's terminal error) is available.
func (b *streamBuffer) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		// Closed wins over buffered data: Close is teardown, and handing out
		// remaining bytes would only delay it.
		if b.closed {
			return 0, io.ErrClosedPipe
		}
		if b.primed && b.size > 0 {
			n := b.size
			if room := len(b.buf) - b.start; n > room {
				n = room
			}
			if n > len(p) {
				n = len(p)
			}
			copy(p, b.buf[b.start:b.start+n])
			b.start = (b.start + n) % len(b.buf)
			b.size -= n
			b.cond.Broadcast() // the writer may be blocked on a full buffer
			return n, nil
		}
		if b.err != nil {
			return 0, b.err
		}
		b.cond.Wait()
	}
}

// Close releases all blocked readers and writers; subsequent reads fail.
// Safe to call multiple times.
func (b *streamBuffer) Close() {
	b.mu.Lock()
	b.closed = true
	b.primed = true
	b.mu.Unlock()
	b.cond.Broadcast()
}
