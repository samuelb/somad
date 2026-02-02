package main

import (
	"testing"
)

func TestBufferStateString(t *testing.T) {
	tests := []struct {
		state    BufferState
		expected string
	}{
		{BufferStateBuffering, "Buffering"},
		{BufferStateHealthy, "Healthy"},
		{BufferStateUnderrun, "Underrun"},
		{BufferStateError, "Error"},
		{BufferStateClosed, "Closed"},
		{BufferState(99), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("BufferState.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewBufferedStream(t *testing.T) {
	url := "http://example.com/stream"
	bs := NewBufferedStream(url)

	if bs.url != url {
		t.Errorf("NewBufferedStream url = %v, want %v", bs.url, url)
	}

	if bs.capacity != DefaultBufferCapacity {
		t.Errorf("NewBufferedStream capacity = %v, want %v", bs.capacity, DefaultBufferCapacity)
	}

	if bs.highWatermark != DefaultHighWatermark {
		t.Errorf("NewBufferedStream highWatermark = %v, want %v", bs.highWatermark, DefaultHighWatermark)
	}

	if bs.lowWatermark != DefaultLowWatermark {
		t.Errorf("NewBufferedStream lowWatermark = %v, want %v", bs.lowWatermark, DefaultLowWatermark)
	}

	if bs.state != BufferStateBuffering {
		t.Errorf("NewBufferedStream state = %v, want %v", bs.state, BufferStateBuffering)
	}

	if !bs.initialFill {
		t.Error("NewBufferedStream initialFill should be true")
	}

	if bs.closed {
		t.Error("NewBufferedStream closed should be false")
	}
}

func TestDefaultReconnectConfig(t *testing.T) {
	cfg := DefaultReconnectConfig()

	if cfg.InitialBackoff <= 0 {
		t.Error("InitialBackoff should be positive")
	}

	if cfg.MaxBackoff <= cfg.InitialBackoff {
		t.Error("MaxBackoff should be greater than InitialBackoff")
	}

	if cfg.BackoffFactor <= 1 {
		t.Error("BackoffFactor should be greater than 1")
	}

	if cfg.MaxRetries != 0 {
		t.Errorf("MaxRetries = %v, want 0 (infinite)", cfg.MaxRetries)
	}
}

func TestBufferedStreamWriteRead(t *testing.T) {
	bs := NewBufferedStream("http://example.com/stream")
	bs.initialFill = false // Skip initial fill requirement for testing

	// Simulate writing data to buffer
	testData := []byte("Hello, World!")
	bs.writeToBuffer(testData)

	// Read data back
	readBuf := make([]byte, len(testData))
	n, err := bs.Read(readBuf)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if n != len(testData) {
		t.Errorf("Read() n = %v, want %v", n, len(testData))
	}

	if string(readBuf) != string(testData) {
		t.Errorf("Read() data = %v, want %v", string(readBuf), string(testData))
	}
}

func TestBufferedStreamClose(t *testing.T) {
	bs := NewBufferedStream("http://example.com/stream")

	err := bs.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if !bs.closed {
		t.Error("Close() should set closed to true")
	}

	// Double close should not error
	err = bs.Close()
	if err != nil {
		t.Errorf("Double Close() error = %v", err)
	}
}

func TestBufferStats(t *testing.T) {
	bs := NewBufferedStream("http://example.com/stream")
	bs.initialFill = false

	// Write some data
	testData := make([]byte, 1024)
	bs.writeToBuffer(testData)

	stats := bs.GetStats()

	expectedFillLevel := float64(1024) / float64(DefaultBufferCapacity)
	if stats.FillLevel != expectedFillLevel {
		t.Errorf("GetStats() FillLevel = %v, want %v", stats.FillLevel, expectedFillLevel)
	}

	if stats.LastError != nil {
		t.Errorf("GetStats() LastError = %v, want nil", stats.LastError)
	}
}
