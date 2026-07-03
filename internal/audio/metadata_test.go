package audio

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseICYMetadata(t *testing.T) {
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
			got, err := parseICYMetadata(tt.input)

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

// icyStreamBuilder assembles a synthetic Shoutcast body: audio segments of
// exactly icyInt bytes, each followed by a metadata block.
type icyStreamBuilder struct {
	buf    bytes.Buffer
	icyInt int
}

// segment appends icyInt bytes of the given audio byte followed by a metadata
// block containing metadata (or a zero-length block when metadata is empty).
func (b *icyStreamBuilder) segment(audioByte byte, metadata string) *icyStreamBuilder {
	b.buf.Write(bytes.Repeat([]byte{audioByte}, b.icyInt))
	if metadata == "" {
		b.buf.WriteByte(0)
		return b
	}
	metaLen := (len(metadata) + 15) / 16
	b.buf.WriteByte(byte(metaLen)) // #nosec G115 // Test helper, metadata length is always small
	b.buf.WriteString(metadata)
	if padding := metaLen*16 - len(metadata); padding > 0 {
		b.buf.Write(bytes.Repeat([]byte{0x00}, padding))
	}
	return b
}

// collectTitles reads the demuxed audio and the titles reported along the way.
func collectTitles(t *testing.T, src io.Reader, icyInt int) (audio []byte, titles []string) {
	t.Helper()
	d := newICYDemuxer(src, icyInt, func(title string) {
		titles = append(titles, title)
	})
	audio, err := io.ReadAll(d)
	require.NoError(t, err, "io.ReadAll treats io.EOF as success")
	return audio, titles
}

func TestICYDemuxer_StripsMetadataAndReportsTitle(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 100}
	b.segment(0xAA, "StreamTitle='Test Song';").segment(0xBB, "")

	audio, titles := collectTitles(t, &b.buf, b.icyInt)

	assert.Equal(t, append(bytes.Repeat([]byte{0xAA}, 100), bytes.Repeat([]byte{0xBB}, 100)...), audio,
		"metadata must be stripped, audio passed through unchanged")
	assert.Equal(t, []string{"Test Song"}, titles)
}

func TestICYDemuxer_LargeInterval(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 8192}
	b.segment(0x01, "StreamTitle='Artist - Track';StreamUrl='';")

	audio, titles := collectTitles(t, &b.buf, b.icyInt)

	assert.Len(t, audio, 8192)
	assert.Equal(t, []string{"Artist - Track"}, titles)
}

func TestICYDemuxer_UnicodeTitle(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 50}
	b.segment(0x01, "StreamTitle='Café — Música';")

	_, titles := collectTitles(t, &b.buf, b.icyInt)

	assert.Equal(t, []string{"Café — Música"}, titles)
}

func TestICYDemuxer_ZeroLengthBlockMeansNoChange(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 10}
	b.segment(0x01, "").segment(0x02, "")

	audio, titles := collectTitles(t, &b.buf, b.icyInt)

	assert.Len(t, audio, 20)
	assert.Empty(t, titles)
}

func TestICYDemuxer_RepeatedTitleReportedOnce(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 10}
	b.segment(0x01, "StreamTitle='Same Song';").
		segment(0x02, "StreamTitle='Same Song';").
		segment(0x03, "StreamTitle='New Song';")

	_, titles := collectTitles(t, &b.buf, b.icyInt)

	assert.Equal(t, []string{"Same Song", "New Song"}, titles)
}

func TestICYDemuxer_MalformedMetadataSkipped(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 10}
	b.segment(0x01, "StreamUrl='http://example.com';").segment(0x02, "StreamTitle='Good';")

	audio, titles := collectTitles(t, &b.buf, b.icyInt)

	assert.Len(t, audio, 20, "audio around malformed metadata must survive")
	assert.Equal(t, []string{"Good"}, titles)
}

func TestICYDemuxer_TruncatedMetadataBlockIsError(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(bytes.Repeat([]byte{0x01}, 10))
	buf.WriteByte(2) // promises 32 metadata bytes...
	buf.WriteString("StreamTitle='cut") // ...but delivers fewer

	d := newICYDemuxer(&buf, 10, nil)
	_, err := io.ReadAll(d)

	assert.Error(t, err)
}

func TestICYDemuxer_NilCallback(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 10}
	b.segment(0x01, "StreamTitle='Ignored';")

	d := newICYDemuxer(&b.buf, b.icyInt, nil)
	audio, err := io.ReadAll(d)

	require.NoError(t, err)
	assert.Len(t, audio, 10)
}

func TestICYDemuxer_SmallReadBuffers(t *testing.T) {
	b := &icyStreamBuilder{icyInt: 7}
	b.segment(0x01, "StreamTitle='Chunked';").segment(0x02, "")

	d := newICYDemuxer(&b.buf, b.icyInt, nil)

	// Read through a 3-byte buffer so reads straddle metadata boundaries.
	var audio []byte
	buf := make([]byte, 3)
	for {
		n, err := d.Read(buf)
		audio = append(audio, buf[:n]...)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}

	assert.Equal(t, append(bytes.Repeat([]byte{0x01}, 7), bytes.Repeat([]byte{0x02}, 7)...), audio)
}

func BenchmarkParseICYMetadata_Standard(b *testing.B) {
	input := "StreamTitle='Artist - Song Title';StreamUrl='';"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseICYMetadata(input)
	}
}

func BenchmarkParseICYMetadata_Unicode(b *testing.B) {
	input := "StreamTitle='Café del Mar - Música Ambiental';StreamUrl='';"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseICYMetadata(input)
	}
}

func BenchmarkParseICYMetadata_MultipleFields(b *testing.B) {
	input := "StreamTitle='The Track';StreamUrl='http://foo';StreamGenre='Jazz';StreamBitrate='128';"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseICYMetadata(input)
	}
}
