package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// BufferState represents the current state of the buffered stream.
type BufferState int

const (
	BufferStateBuffering BufferState = iota
	BufferStateHealthy
	BufferStateUnderrun
	BufferStateError
	BufferStateClosed
)

func (s BufferState) String() string {
	switch s {
	case BufferStateBuffering:
		return "Buffering"
	case BufferStateHealthy:
		return "Healthy"
	case BufferStateUnderrun:
		return "Underrun"
	case BufferStateError:
		return "Error"
	case BufferStateClosed:
		return "Closed"
	default:
		return "Unknown"
	}
}

// BufferStats contains statistics about the buffer state.
type BufferStats struct {
	FillLevel float64     // 0.0 to 1.0
	State     BufferState
	LastError error
}

// ReconnectConfig controls reconnection backoff behavior.
type ReconnectConfig struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
	MaxRetries     int // 0 means infinite
}

// DefaultReconnectConfig returns sensible defaults for radio streaming.
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
		MaxRetries:     0, // Infinite retries for radio
	}
}

// Buffer size constants
const (
	DefaultBufferCapacity   = 256 * 1024 // 256 KB (~15 seconds at 128kbps)
	DefaultHighWatermark    = 32 * 1024  // Start playback after ~2 seconds at 128kbps
	DefaultLowWatermark     = 16 * 1024  // Recover from underrun threshold
	DefaultReadChunkSize    = 8 * 1024   // Read from HTTP in 8KB chunks
	DefaultStatsChannelSize = 32         // Buffer for stats channel
	DefaultStatsInterval    = 8          // Emit stats every N writes
)

// BufferedStream wraps an HTTP stream with a ring buffer for smooth playback.
type BufferedStream struct {
	url             string
	reconnectConfig ReconnectConfig

	// Ring buffer
	buf      []byte
	capacity int
	readPos  int
	writePos int
	filled   int

	// Synchronization
	mu       sync.Mutex
	cond     *sync.Cond
	closed   bool
	stopChan chan struct{}

	// HTTP connection
	resp   *http.Response
	client *http.Client

	// Watermarks
	highWatermark int
	lowWatermark  int

	// State tracking
	state       BufferState
	lastError   error
	statsChan   chan BufferStats
	initialFill bool // True until we've reached high watermark once
	writeCount  int  // Counter for periodic stats emission
}

// NewBufferedStream creates a new buffered stream for the given URL.
func NewBufferedStream(url string) *BufferedStream {
	bs := &BufferedStream{
		url:             url,
		reconnectConfig: DefaultReconnectConfig(),
		buf:             make([]byte, DefaultBufferCapacity),
		capacity:        DefaultBufferCapacity,
		highWatermark:   DefaultHighWatermark,
		lowWatermark:    DefaultLowWatermark,
		stopChan:        make(chan struct{}),
		client:          &http.Client{Timeout: 30 * time.Second},
		state:           BufferStateBuffering,
		statsChan:       make(chan BufferStats, DefaultStatsChannelSize),
		initialFill:     true,
	}
	bs.cond = sync.NewCond(&bs.mu)
	return bs
}

// Start begins the fill goroutine and returns a channel for buffer state updates.
func (bs *BufferedStream) Start() (<-chan BufferStats, error) {
	// Perform initial connection
	if err := bs.connect(); err != nil {
		return nil, fmt.Errorf("initial connection failed: %w", err)
	}

	// Start the fill goroutine
	go bs.fillLoop()

	return bs.statsChan, nil
}

// connect establishes an HTTP connection to the stream URL.
func (bs *BufferedStream) connect() error {
	req, err := http.NewRequest("GET", bs.url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := bs.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	bs.resp = resp
	return nil
}

// fillLoop continuously reads from HTTP and fills the buffer.
func (bs *BufferedStream) fillLoop() {
	defer func() {
		bs.mu.Lock()
		bs.closed = true
		bs.state = BufferStateClosed
		bs.cond.Broadcast()
		bs.mu.Unlock()
		close(bs.statsChan)
	}()

	chunk := make([]byte, DefaultReadChunkSize)
	backoff := bs.reconnectConfig.InitialBackoff
	retries := 0

	for {
		select {
		case <-bs.stopChan:
			return
		default:
		}

		// Read from HTTP response
		n, err := bs.resp.Body.Read(chunk)

		if n > 0 {
			bs.writeToBuffer(chunk[:n])
			backoff = bs.reconnectConfig.InitialBackoff // Reset backoff on successful read
			retries = 0
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				// Stream ended (shouldn't happen for radio, but handle it)
				bs.setError(fmt.Errorf("stream ended unexpectedly"))
				return
			}

			// Connection error - attempt reconnection
			bs.resp.Body.Close()
			bs.resp = nil

			if bs.reconnectConfig.MaxRetries > 0 && retries >= bs.reconnectConfig.MaxRetries {
				bs.setError(fmt.Errorf("max retries exceeded: %w", err))
				return
			}

			bs.updateState(BufferStateUnderrun)

			// Wait before reconnecting
			select {
			case <-bs.stopChan:
				return
			case <-time.After(backoff):
			}

			// Attempt reconnection
			if reconnectErr := bs.connect(); reconnectErr != nil {
				retries++
				backoff = time.Duration(float64(backoff) * bs.reconnectConfig.BackoffFactor)
				if backoff > bs.reconnectConfig.MaxBackoff {
					backoff = bs.reconnectConfig.MaxBackoff
				}
				continue
			}

			// Reconnection successful
			bs.mu.Lock()
			if bs.filled >= bs.lowWatermark {
				bs.state = BufferStateHealthy
			} else {
				bs.state = BufferStateBuffering
			}
			bs.mu.Unlock()
		}
	}
}

// writeToBuffer adds data to the ring buffer.
func (bs *BufferedStream) writeToBuffer(data []byte) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	for len(data) > 0 {
		// Calculate available space
		available := bs.capacity - bs.filled
		if available == 0 {
			// Buffer full - wait for consumer to read
			bs.cond.Wait()
			continue
		}

		// Calculate how much we can write in one chunk
		toWrite := len(data)
		if toWrite > available {
			toWrite = available
		}

		// Handle wrap-around
		endSpace := bs.capacity - bs.writePos
		if toWrite <= endSpace {
			copy(bs.buf[bs.writePos:], data[:toWrite])
			bs.writePos = (bs.writePos + toWrite) % bs.capacity
		} else {
			// Write in two parts
			copy(bs.buf[bs.writePos:], data[:endSpace])
			copy(bs.buf[0:], data[endSpace:toWrite])
			bs.writePos = toWrite - endSpace
		}

		bs.filled += toWrite
		data = data[toWrite:]

		// Update state based on fill level
		if bs.initialFill && bs.filled >= bs.highWatermark {
			bs.initialFill = false
			bs.state = BufferStateHealthy
			bs.emitStats()
		} else if !bs.initialFill && bs.state == BufferStateUnderrun && bs.filled >= bs.lowWatermark {
			bs.state = BufferStateHealthy
			bs.emitStats()
		}

		// Emit periodic stats during normal playback
		bs.writeCount++
		if bs.writeCount >= DefaultStatsInterval {
			bs.writeCount = 0
			bs.emitStats()
		}

		bs.cond.Broadcast()
	}
}

// Read implements io.Reader for the buffered stream.
func (bs *BufferedStream) Read(p []byte) (n int, err error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Wait for data or initial buffer fill
	for {
		if bs.closed {
			if bs.lastError != nil {
				return 0, bs.lastError
			}
			return 0, io.EOF
		}

		// During initial fill, wait for high watermark
		if bs.initialFill {
			if bs.filled < bs.highWatermark {
				bs.emitStats()
				bs.cond.Wait()
				continue
			}
		}

		// Normal operation - if we have data, read it
		if bs.filled > 0 {
			break
		}

		// Buffer underrun
		if bs.state != BufferStateUnderrun && bs.state != BufferStateBuffering {
			bs.state = BufferStateUnderrun
			bs.emitStats()
		}
		bs.cond.Wait()
	}

	// Read from buffer
	toRead := len(p)
	if toRead > bs.filled {
		toRead = bs.filled
	}

	// Handle wrap-around
	endSpace := bs.capacity - bs.readPos
	if toRead <= endSpace {
		copy(p, bs.buf[bs.readPos:bs.readPos+toRead])
		bs.readPos = (bs.readPos + toRead) % bs.capacity
	} else {
		// Read in two parts
		copy(p, bs.buf[bs.readPos:])
		copy(p[endSpace:], bs.buf[0:toRead-endSpace])
		bs.readPos = toRead - endSpace
	}

	bs.filled -= toRead
	bs.cond.Broadcast()

	return toRead, nil
}

// Close stops the fill goroutine and closes the HTTP connection.
func (bs *BufferedStream) Close() error {
	bs.mu.Lock()
	if bs.closed {
		bs.mu.Unlock()
		return nil
	}
	bs.mu.Unlock()

	close(bs.stopChan)

	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.resp != nil {
		bs.resp.Body.Close()
	}

	bs.closed = true
	bs.cond.Broadcast()

	return nil
}

// setError sets an error state and notifies waiters.
func (bs *BufferedStream) setError(err error) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	bs.lastError = err
	bs.state = BufferStateError
	bs.emitStats()
	bs.cond.Broadcast()
}

// updateState updates the buffer state.
func (bs *BufferedStream) updateState(state BufferState) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	if bs.state != state {
		bs.state = state
		bs.emitStats()
	}
}

// emitStats sends current stats to the channel (non-blocking).
// Must be called with mutex held.
func (bs *BufferedStream) emitStats() {
	stats := BufferStats{
		FillLevel: float64(bs.filled) / float64(bs.capacity),
		State:     bs.state,
		LastError: bs.lastError,
	}

	select {
	case bs.statsChan <- stats:
	default:
		// Channel full, drop the update
	}
}

// GetStats returns current buffer statistics.
func (bs *BufferedStream) GetStats() BufferStats {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	return BufferStats{
		FillLevel: float64(bs.filled) / float64(bs.capacity),
		State:     bs.state,
		LastError: bs.lastError,
	}
}
