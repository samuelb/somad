package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseICYMetadata(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "standard format",
			input:   "StreamTitle='Artist - Song Title';StreamUrl='';",
			want:    "Artist - Song Title",
			wantErr: false,
		},
		{
			name:    "title only",
			input:   "StreamTitle='Just a Title';",
			want:    "Just a Title",
			wantErr: false,
		},
		{
			name:    "empty title",
			input:   "StreamTitle='';",
			want:    "",
			wantErr: false,
		},
		{
			name:    "with extra spaces",
			input:   "StreamTitle='  Spaced Artist - Spaced Title  ';",
			want:    "Spaced Artist - Spaced Title",
			wantErr: false,
		},
		{
			name:    "no StreamTitle",
			input:   "StreamUrl='http://example.com';",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "multiple fields",
			input:   "StreamTitle='The Track';StreamUrl='http://foo';StreamGenre='Jazz';",
			want:    "The Track",
			wantErr: false,
		},
		{
			name:    "title with special characters",
			input:   "StreamTitle='Artist (feat. Other) - Song [Remix]';",
			want:    "Artist (feat. Other) - Song [Remix]",
			wantErr: false,
		},
		{
			name:    "unicode characters",
			input:   "StreamTitle='Café del Mar - Música Ambiental';",
			want:    "Café del Mar - Música Ambiental",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mr.parseICYMetadata(tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("parseICYMetadata() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && got.Title != tt.want {
				t.Errorf("parseICYMetadata() title = %v, want %v", got.Title, tt.want)
			}
		})
	}
}

func TestNewMetadataReader(t *testing.T) {
	url := "http://example.com/stream"
	mr := NewMetadataReader(url)

	if mr.url != url {
		t.Errorf("NewMetadataReader url = %v, want %v", mr.url, url)
	}

	if mr.client == nil {
		t.Error("NewMetadataReader client should not be nil")
	}

	if mr.stopChan == nil {
		t.Error("NewMetadataReader stopChan should not be nil")
	}

	if mr.updateChan == nil {
		t.Error("NewMetadataReader updateChan should not be nil")
	}
}

func TestMetadataReaderGetUpdateChan(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")
	ch := mr.GetUpdateChan()

	if ch == nil {
		t.Error("GetUpdateChan() should not return nil")
	}
}

// buildICYStream constructs a byte buffer simulating an ICY audio stream.
// It writes icyInt bytes of dummy audio data, then a metadata block.
func buildICYStream(icyInt int, metadata string) *bytes.Buffer {
	buf := new(bytes.Buffer)
	// Dummy audio data
	buf.Write(bytes.Repeat([]byte{0xFF}, icyInt))
	// Metadata length byte (in 16-byte units)
	metaLen := (len(metadata) + 15) / 16
	buf.WriteByte(byte(metaLen))
	// Metadata padded with null bytes to fill metaLen*16 bytes
	buf.WriteString(metadata)
	padding := metaLen*16 - len(metadata)
	if padding > 0 {
		buf.Write(bytes.Repeat([]byte{0x00}, padding))
	}
	return buf
}

// newICYServer creates an httptest server that serves an ICY-format response.
func newICYServer(icyInt int, metadata string) *httptest.Server {
	body := buildICYStream(icyInt, metadata)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-metaint", strconv.Itoa(icyInt))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body.Bytes())
	}))
}

func TestReadICYMetadata(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")

	tests := []struct {
		name     string
		icyInt   int
		metadata string
		want     string
		wantErr  bool
	}{
		{
			name:     "standard metadata",
			icyInt:   100,
			metadata: "StreamTitle='Test Song';",
			want:     "Test Song",
		},
		{
			name:     "large icy interval",
			icyInt:   8192,
			metadata: "StreamTitle='Artist - Track';StreamUrl='';",
			want:     "Artist - Track",
		},
		{
			name:     "unicode metadata",
			icyInt:   50,
			metadata: "StreamTitle='Café — Música';",
			want:     "Café — Música",
		},
		{
			name:     "no stream title in metadata",
			icyInt:   100,
			metadata: "StreamUrl='http://example.com';",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := buildICYStream(tt.icyInt, tt.metadata)
			info, err := mr.readICYMetadata(buf, strconv.Itoa(tt.icyInt))

			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, info.Title)
		})
	}
}

func TestReadICYMetadata_InvalidIcyInt(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")
	buf := buildICYStream(100, "StreamTitle='Test';")

	_, err := mr.readICYMetadata(buf, "not-a-number")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid icy-metaint")
}

func TestReadICYMetadata_ZeroLengthMetadata(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")

	// Build a stream where the metadata length byte is 0
	buf := new(bytes.Buffer)
	buf.Write(bytes.Repeat([]byte{0xFF}, 100))
	buf.WriteByte(0) // metadata length = 0

	_, err := mr.readICYMetadata(buf, "100")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no metadata")
}

func TestGetMetadata(t *testing.T) {
	server := newICYServer(100, "StreamTitle='Server Song';")
	defer server.Close()

	mr := NewMetadataReader(server.URL)
	info, err := mr.getMetadata()

	require.NoError(t, err)
	assert.Equal(t, "Server Song", info.Title)
}

func TestGetMetadata_VerifiesHeaders(t *testing.T) {
	var gotUserAgent, gotIcyMetaData string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		gotIcyMetaData = r.Header.Get("Icy-MetaData")

		body := buildICYStream(50, "StreamTitle='Test';")
		w.Header().Set("icy-metaint", "50")
		_, _ = w.Write(body.Bytes())
	}))
	defer server.Close()

	mr := NewMetadataReader(server.URL)
	_, err := mr.getMetadata()
	require.NoError(t, err)

	assert.Equal(t, userAgent(), gotUserAgent)
	assert.Equal(t, "1", gotIcyMetaData)
}

func TestGetMetadata_NoIcyMetaint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No icy-metaint header
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("audio data"))
	}))
	defer server.Close()

	mr := NewMetadataReader(server.URL)
	_, err := mr.getMetadata()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ICY metadata")
}

func TestGetMetadata_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	mr := NewMetadataReader(server.URL)
	_, err := mr.getMetadata()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestMetadataReaderStartStop(t *testing.T) {
	server := newICYServer(50, "StreamTitle='Live Track';")
	defer server.Close()

	mr := NewMetadataReader(server.URL)
	mr.Start()

	// Should receive the initial metadata update
	select {
	case info := <-mr.GetUpdateChan():
		assert.Equal(t, "Live Track", info.Title)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for metadata update")
	}

	// Stop should not panic and should be safe to call multiple times
	mr.Stop()
	mr.Stop()
}

func TestMetadataReaderStop_BeforeStart(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")
	// Stop without Start should not panic
	mr.Stop()
	mr.Stop()
}

func BenchmarkParseICYMetadata_Standard(b *testing.B) {
	mr := NewMetadataReader("http://example.com/stream")
	input := "StreamTitle='Artist - Song Title';StreamUrl='';"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mr.parseICYMetadata(input)
	}
}

func BenchmarkParseICYMetadata_Unicode(b *testing.B) {
	mr := NewMetadataReader("http://example.com/stream")
	input := "StreamTitle='Café del Mar - Música Ambiental';StreamUrl='';"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mr.parseICYMetadata(input)
	}
}

func BenchmarkParseICYMetadata_MultipleFields(b *testing.B) {
	mr := NewMetadataReader("http://example.com/stream")
	input := "StreamTitle='The Track';StreamUrl='http://foo';StreamGenre='Jazz';StreamBitrate='128';"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = mr.parseICYMetadata(input)
	}
}
