package audio

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/iotest"
	"time"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silentMP3FramePCM is the size of what one of silentMP3Frames' frames
// decodes to: 1152 samples of 16-bit stereo.
const silentMP3FramePCM = 1152 * 4

// mp3FrameTail is what joining a stream between two headers leaves in front
// of the first whole frame: the end of a frame's data, here with four bytes
// in it that pass for a frame header.
func mp3FrameTail(falseHeader ...byte) []byte {
	tail := make([]byte, 97)
	copy(tail[20:], falseHeader)
	return tail
}

func TestMP3Decoder_FalseHeaderBeforeTheFirstFrame(t *testing.T) {
	const frames = 8
	for _, tc := range []struct {
		name   string
		header []byte
	}{
		// Layer III at another rate than the stream's 44.1 kHz: go-mp3 took
		// the rate from it, and the whole stream was resampled from there.
		{"48 kHz", []byte{0xFF, 0xFB, 0x94, 0x64}},
		{"32 kHz", []byte{0xFF, 0xFB, 0x98, 0x64}},
		{"MPEG-2 at 22.05 kHz", []byte{0xFF, 0xF3, 0x90, 0x64}},
		// Kinds go-mp3 does not decode: it failed on them, and the connect
		// with it.
		{"layer II", []byte{0xFF, 0xFD, 0x90, 0x64}},
		{"MPEG 2.5", []byte{0xFF, 0xE3, 0x90, 0x64}},
		{"free bitrate", []byte{0xFF, 0xFB, 0x00, 0x64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := append(mp3FrameTail(tc.header...), silentMP3Frames(frames)...)
			dec, err := newDecoder(FormatMP3, bytes.NewReader(stream))
			require.NoError(t, err)
			assert.Equal(t, 44100, dec.SampleRate())
			pcm, err := io.ReadAll(dec)
			require.NoError(t, err)
			assert.Equal(t, frames*silentMP3FramePCM, len(pcm), "every whole frame decodes, and nothing else")
		})
	}
}

func TestMP3Sync_DiscardsUpToTheFirstFrame(t *testing.T) {
	frames := silentMP3Frames(4)
	stream := append(mp3FrameTail(0xFF, 0xFB, 0x94, 0x64), frames...)
	got, err := io.ReadAll(newMP3Sync(bytes.NewReader(stream)))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(frames, got), "the stream must come out whole from its first frame on")
}

func TestMP3Sync_CleanStreamPassesThroughUntouched(t *testing.T) {
	// With padded frames and a change of bitrate, as a real stream has
	// them; the last frame has no header after it to confirm it.
	var stream []byte
	for _, h := range [][4]byte{
		{0xFF, 0xFB, 0x90, 0x64}, // 128 kbit/s, 417 bytes
		{0xFF, 0xFB, 0x92, 0x64}, // padded, 418
		{0xFF, 0xFB, 0xA0, 0x64}, // 160 kbit/s, 522
		{0xFF, 0xFA, 0x90, 0xC4}, // with a CRC, mono
		{0xFF, 0xFB, 0x90, 0x64},
	} {
		n, ok := mp3FrameLen(h[:])
		require.True(t, ok)
		frame := bytes.Repeat([]byte{0x55}, n) // no byte of it starts a header
		copy(frame, h[:])
		stream = append(stream, frame...)
	}
	for name, wrap := range map[string]func(io.Reader) io.Reader{
		"all at once":    func(r io.Reader) io.Reader { return r },
		"byte by byte":   iotest.OneByteReader,
		"data with EOF":  iotest.DataErrReader,
		"in half slices": iotest.HalfReader,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := io.ReadAll(newMP3Sync(wrap(bytes.NewReader(stream))))
			require.NoError(t, err)
			assert.True(t, bytes.Equal(stream, got), "a stream that starts on a frame must not lose or gain a byte")
		})
	}
}

func TestMP3Sync_WaitsForNoMoreThanTheNextHeader(t *testing.T) {
	// A live stream: the first frame and the four bytes after it have
	// arrived, and the rest of the second frame is still on its way.
	const frameLen = 417
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	go func() { _, _ = pw.Write(silentMP3Frames(2)[:frameLen+mp3HeaderLen]) }()

	read := make(chan int, 1)
	go func() {
		n, _ := newMP3Sync(pr).Read(make([]byte, frameLen))
		read <- n
	}()
	select {
	case n := <-read:
		assert.Equal(t, frameLen, n, "the first frame is ready to decode")
	case <-time.After(2 * time.Second):
		t.Fatal("the first frame is held back for more than the header that confirms it")
	}
}

func TestMP3Sync_StreamEndingBeforeTheNextHeader(t *testing.T) {
	// Nothing confirms a lone frame, and nothing contradicts it either: it
	// passes, as it would have without the check.
	one := silentMP3Frames(1)
	got, err := io.ReadAll(newMP3Sync(bytes.NewReader(one)))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(one, got))

	// Too short to hold a header at all: the stream's end, as before.
	for _, short := range [][]byte{nil, {0xFF}, {0xFF, 0xFB, 0x90}} {
		n, err := newMP3Sync(bytes.NewReader(short)).Read(make([]byte, 16))
		assert.Zero(t, n)
		assert.ErrorIs(t, err, io.EOF)
	}

	// A source that fails is reported as that failure, not as lost sync.
	_, err = newMP3Sync(iotest.ErrReader(io.ErrClosedPipe)).Read(make([]byte, 16))
	assert.ErrorIs(t, err, io.ErrClosedPipe)
}

// id3v2Tag returns an ID3v2.4 tag with the given body.
func id3v2Tag(body []byte) []byte {
	n := len(body)
	tag := make([]byte, 0, id3v2HeaderLen+n)
	tag = append(tag, 'I', 'D', '3', 4, 0, 0, byte(n>>21&0x7F), byte(n>>14&0x7F), byte(n>>7&0x7F), byte(n&0x7F))
	return append(tag, body...)
}

func TestMP3Sync_SkipsAnID3v2Tag(t *testing.T) {
	const frames = 4
	// A tag longer than the search for a frame goes on for, and full of
	// 48 kHz headers that confirm one another a frame length apart: it has
	// to be skipped as a tag, not searched.
	body := bytes.Repeat([]byte{0xFF, 0xFB, 0x94, 0x64}, mp3MaxSkip/2)
	audio := silentMP3Frames(frames)
	stream := append(id3v2Tag(body), audio...)

	got, err := io.ReadAll(newMP3Sync(bytes.NewReader(stream)))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(audio, got), "the stream must start at the first frame after the tag")

	dec, err := newDecoder(FormatMP3, bytes.NewReader(stream))
	require.NoError(t, err)
	assert.Equal(t, 44100, dec.SampleRate())
	pcm, err := io.ReadAll(dec)
	require.NoError(t, err)
	assert.Equal(t, frames*silentMP3FramePCM, len(pcm))
}

func TestMP3Sync_BytesThatOnlyLookLikeATagAreSearched(t *testing.T) {
	// "ID3" followed by a size no tag header can hold (its bytes are seven
	// bits each) is not a tag, and must not be skipped at a size made up
	// from it: the frames right behind it would go with it.
	audio := silentMP3Frames(4)
	stream := append([]byte{'I', 'D', '3', 4, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}, audio...)
	got, err := io.ReadAll(newMP3Sync(bytes.NewReader(stream)))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(audio, got))
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// endless reads as an endless run of one byte.
type endless byte

func (e endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(e)
	}
	return len(p), nil
}

// noise returns n bytes of reproducible noise (xorshift).
func noise(n int) []byte {
	b := make([]byte, n)
	x := uint32(2463534242)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x >> 24)
	}
	return b
}

func TestMP3Sync_GivesUpOnAStreamThatIsNotMP3(t *testing.T) {
	for name, src := range map[string]io.Reader{
		// Data keeps flowing, so nothing but the bound ends the search.
		"zeros":      endless(0),
		"syncwords":  endless(0xFF),
		"noise":      bytes.NewReader(noise(4 * mp3MaxSkip)),
		"error page": bytes.NewReader(bytes.Repeat([]byte("<html>service unavailable</html>\n"), 4096)),
	} {
		t.Run(name, func(t *testing.T) {
			counted := &countingReader{r: src}
			s := newMP3Sync(counted)
			_, err := s.Read(make([]byte, 16))
			require.ErrorIs(t, err, errMP3LostSync)
			// The search reads ahead by what it buffers, and no further.
			assert.LessOrEqual(t, counted.n, mp3MaxSkip+4096)

			// The failure stays: another read must not search on.
			read := counted.n
			_, err = s.Read(make([]byte, 16))
			require.ErrorIs(t, err, errMP3LostSync)
			assert.Equal(t, read, counted.n)
		})
	}

	// It is how a stream that is not MP3 fails to decode.
	_, err := newDecoder(FormatMP3, endless(0))
	assert.ErrorIs(t, err, errMP3LostSync)
}

func TestPlay_StreamThatIsNotMP3FailsBeforeTheConnectDeadline(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// More than the search covers, then silence on an open connection:
	// without the bound only the connect deadline, seconds away, would end
	// the attempt, and the next mirror or format would wait that long.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2*mp3MaxSkip))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	p := newTestPlayer()

	err := awaitPlay(t, playAsync(p, server.URL, FormatMP3, testGen.Add(1)), 3*time.Second)
	require.ErrorIs(t, err, errMP3LostSync)
	assert.Contains(t, err.Error(), "failed to decode mp3 stream")
}

func TestMP3Sync_DoesNotAllocateOnceSynced(t *testing.T) {
	s := newMP3Sync(bytes.NewReader(silentMP3Frames(2048)))
	buf := make([]byte, 512)
	_, err := s.Read(buf)
	require.NoError(t, err)
	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := s.Read(buf); err != nil {
			t.Fatal(err)
		}
	})
	assert.Zero(t, allocs)
}

func TestMP3FrameLen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header [4]byte
		want   int // 0: not the header of a frame go-mp3 decodes
	}{
		{"MPEG-1, 128 kbit/s at 44.1 kHz", [4]byte{0xFF, 0xFB, 0x90, 0x64}, 417},
		{"the same, padded", [4]byte{0xFF, 0xFB, 0x92, 0x64}, 418},
		{"the same, with a CRC", [4]byte{0xFF, 0xFA, 0x90, 0x64}, 417},
		{"MPEG-1, 128 kbit/s at 48 kHz", [4]byte{0xFF, 0xFB, 0x94, 0x64}, 384},
		{"MPEG-1, 128 kbit/s at 32 kHz", [4]byte{0xFF, 0xFB, 0x98, 0x64}, 576},
		{"MPEG-1, 320 kbit/s at 32 kHz, padded", [4]byte{0xFF, 0xFB, 0xEA, 0x64}, 1441},
		{"MPEG-2, 80 kbit/s at 22.05 kHz", [4]byte{0xFF, 0xF3, 0x90, 0x64}, 261},
		{"MPEG-2, 64 kbit/s at 24 kHz, padded", [4]byte{0xFF, 0xF3, 0x86, 0x64}, 193},
		{"MPEG-2, 8 kbit/s at 24 kHz", [4]byte{0xFF, 0xF3, 0x14, 0x64}, 24},
		{"no syncword", [4]byte{0xFF, 0xDB, 0x90, 0x64}, 0},
		{"MPEG 2.5", [4]byte{0xFF, 0xE3, 0x90, 0x64}, 0},
		{"reserved version", [4]byte{0xFF, 0xEB, 0x90, 0x64}, 0},
		{"layer I", [4]byte{0xFF, 0xFF, 0x90, 0x64}, 0},
		{"layer II", [4]byte{0xFF, 0xFD, 0x90, 0x64}, 0},
		{"reserved layer", [4]byte{0xFF, 0xF9, 0x90, 0x64}, 0},
		{"free bitrate", [4]byte{0xFF, 0xFB, 0x00, 0x64}, 0},
		{"invalid bitrate", [4]byte{0xFF, 0xFB, 0xF0, 0x64}, 0},
		{"reserved sample rate", [4]byte{0xFF, 0xFB, 0x9C, 0x64}, 0},
		{"reserved emphasis", [4]byte{0xFF, 0xFB, 0x90, 0x66}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mp3FrameLen(tc.header[:])
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.want != 0, ok)
		})
	}
}
