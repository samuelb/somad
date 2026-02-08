package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const metadataCheckInterval = 10 * time.Second

// TrackInfo represents the current track information from ICY metadata.
type TrackInfo struct {
	Title string
}

// MetadataReader reads and monitors MP3 metadata from a stream.
type MetadataReader struct {
	url        string
	client     *http.Client
	stopChan   chan struct{}
	stopOnce   sync.Once
	updateChan chan TrackInfo
}

// NewMetadataReader creates a new metadata reader for the given stream URL.
func NewMetadataReader(url string) *MetadataReader {
	return &MetadataReader{
		url:        url,
		client:     &http.Client{Timeout: 15 * time.Second},
		stopChan:   make(chan struct{}),
		updateChan: make(chan TrackInfo, 1),
	}
}

// Start begins monitoring the stream for metadata updates.
func (mr *MetadataReader) Start() {
	go func() {
		ticker := time.NewTicker(metadataCheckInterval)
		defer ticker.Stop()

		// Get initial metadata
		if trackInfo, err := mr.getMetadata(); err == nil {
			mr.updateChan <- trackInfo
		}

		for {
			select {
			case <-ticker.C:
				if trackInfo, err := mr.getMetadata(); err == nil {
					mr.updateChan <- trackInfo
				}
			case <-mr.stopChan:
				return
			}
		}
	}()
}

// Stop halts the metadata monitoring. Safe to call multiple times.
func (mr *MetadataReader) Stop() {
	mr.stopOnce.Do(func() {
		close(mr.stopChan)
	})
}

// GetUpdateChan returns the channel for receiving metadata updates.
func (mr *MetadataReader) GetUpdateChan() <-chan TrackInfo {
	return mr.updateChan
}

// getMetadata fetches ICY metadata directly from the MP3 stream.
func (mr *MetadataReader) getMetadata() (TrackInfo, error) {
	req, err := http.NewRequest("GET", mr.url, nil)
	if err != nil {
		return TrackInfo{}, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "SomaTUI/1.0")
	req.Header.Set("Icy-MetaData", "1") // Request metadata

	resp, err := mr.client.Do(req)
	if err != nil {
		return TrackInfo{}, fmt.Errorf("failed to fetch stream: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return TrackInfo{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Check if the stream supports ICY metadata
	icyInt := resp.Header.Get("icy-metaint")
	if icyInt == "" {
		return TrackInfo{}, fmt.Errorf("stream does not support ICY metadata")
	}

	// Read ICY metadata
	return mr.readICYMetadata(resp.Body, icyInt)
}

// readICYMetadata reads ICY metadata from the stream.
func (mr *MetadataReader) readICYMetadata(body io.Reader, icyIntStr string) (TrackInfo, error) {
	icyInt, err := strconv.Atoi(icyIntStr)
	if err != nil {
		return TrackInfo{}, fmt.Errorf("invalid icy-metaint value: %w", err)
	}

	reader := bufio.NewReader(body)

	// Skip the first audio block
	_, err = reader.Discard(icyInt)
	if err != nil {
		return TrackInfo{}, fmt.Errorf("failed to skip audio block: %w", err)
	}

	// Read the metadata length byte
	metaLenByte, err := reader.ReadByte()
	if err != nil {
		return TrackInfo{}, fmt.Errorf("failed to read metadata length: %w", err)
	}

	metaLen := int(metaLenByte) * 16
	if metaLen == 0 {
		return TrackInfo{}, fmt.Errorf("no metadata available")
	}

	// Read the metadata block
	metadata := make([]byte, metaLen)
	_, err = io.ReadFull(reader, metadata)
	if err != nil {
		return TrackInfo{}, fmt.Errorf("failed to read metadata block: %w", err)
	}

	// Parse the metadata string
	metaStr := strings.TrimRight(string(metadata), "\x00")
	return mr.parseICYMetadata(metaStr)
}

// parseICYMetadata parses ICY metadata string and extracts the title.
func (mr *MetadataReader) parseICYMetadata(metaStr string) (TrackInfo, error) {
	// ICY metadata format: StreamTitle='Title';StreamUrl='';
	parts := strings.Split(metaStr, ";")

	for _, part := range parts {
		if strings.HasPrefix(part, "StreamTitle='") {
			title := strings.TrimPrefix(part, "StreamTitle='")
			title = strings.TrimSuffix(title, "'")

			// Return the title as-is without parsing
			return TrackInfo{
				Title: strings.TrimSpace(title),
			}, nil
		}
	}

	return TrackInfo{}, fmt.Errorf("no StreamTitle found in metadata")
}
