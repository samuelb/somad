package audio

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// encodeFrames encodes stereo sample pairs as 16-bit LE PCM.
func encodeFrames(frames [][2]int16) []byte {
	buf := make([]byte, 0, len(frames)*bytesPerFrame)
	for _, f := range frames {
		buf = binary.LittleEndian.AppendUint16(buf, uint16(f[0])) // #nosec G115 -- deliberate two's-complement encode of PCM
		buf = binary.LittleEndian.AppendUint16(buf, uint16(f[1])) // #nosec G115 -- deliberate two's-complement encode of PCM
	}
	return buf
}

// decodeFrames decodes 16-bit LE PCM back into stereo sample pairs.
func decodeFrames(t *testing.T, data []byte) [][2]int16 {
	t.Helper()
	require.Zero(t, len(data)%bytesPerFrame, "output must contain whole frames")
	frames := make([][2]int16, 0, len(data)/bytesPerFrame)
	for i := 0; i < len(data); i += bytesPerFrame {
		frames = append(frames, [2]int16{
			int16(binary.LittleEndian.Uint16(data[i : i+2])),   // #nosec G115 -- deliberate two's-complement decode of PCM
			int16(binary.LittleEndian.Uint16(data[i+2 : i+4])), // #nosec G115 -- deliberate two's-complement decode of PCM
		})
	}
	return frames
}

func TestResampler_Upsample_Interpolates(t *testing.T) {
	// 2x upsampling of a ramp: midpoints must appear between source samples.
	src := encodeFrames([][2]int16{{0, 0}, {100, -100}, {200, -200}})
	r := newResampler(bytes.NewReader(src), 22050, 44100)

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	frames := decodeFrames(t, out)

	// Positions advance by 0.5 source frames per output frame.
	want := [][2]int16{{0, 0}, {50, -50}, {100, -100}, {150, -150}, {200, -200}}
	require.GreaterOrEqual(t, len(frames), len(want))
	assert.Equal(t, want, frames[:len(want)])
}

func TestResampler_Downsample_HalvesFrameCount(t *testing.T) {
	frames := make([][2]int16, 100)
	for i := range frames {
		frames[i] = [2]int16{int16(i), int16(-i)}
	}
	r := newResampler(bytes.NewReader(encodeFrames(frames)), 48000, 24000)

	out, err := io.ReadAll(r)
	require.NoError(t, err)

	got := decodeFrames(t, out)
	assert.InDelta(t, 50, len(got), 2, "downsampling by 2 should roughly halve the frame count")
	// Every output frame skips one source frame.
	assert.Equal(t, [2]int16{0, 0}, got[0])
	assert.Equal(t, [2]int16{2, -2}, got[1])
}

func TestResampler_SameRatePassesThrough(t *testing.T) {
	src := [][2]int16{{1, -1}, {2, -2}, {3, -3}}
	r := newResampler(bytes.NewReader(encodeFrames(src)), 44100, 44100)

	out, err := io.ReadAll(r)
	require.NoError(t, err)

	assert.Equal(t, src, decodeFrames(t, out))
}

func TestResampler_ZeroSourceRateTerminates(t *testing.T) {
	// A malformed decoder reporting rate 0 must not yield an infinite stream
	// (step 0 would repeat the first frame forever without ever erroring).
	src := [][2]int16{{1, -1}, {2, -2}, {3, -3}}
	r := newResampler(bytes.NewReader(encodeFrames(src)), 0, 44100)

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, src, decodeFrames(t, out), "rate 0 falls back to pass-through")
}

func TestResampler_EmptySource(t *testing.T) {
	r := newResampler(bytes.NewReader(nil), 22050, 44100)

	out, err := io.ReadAll(r)
	require.NoError(t, err, "io.ReadAll treats io.EOF as success")
	assert.Empty(t, out)
}

func TestResampler_SingleFrameSource(t *testing.T) {
	r := newResampler(bytes.NewReader(encodeFrames([][2]int16{{42, -42}})), 22050, 44100)

	out, err := io.ReadAll(r)
	require.NoError(t, err)

	frames := decodeFrames(t, out)
	require.NotEmpty(t, frames)
	for _, f := range frames {
		assert.Equal(t, [2]int16{42, -42}, f, "a single frame should be held flat")
	}
}

func TestResampler_TruncatedFrameIsEOF(t *testing.T) {
	// Two whole frames plus a dangling byte: the tail must be dropped cleanly.
	src := append(encodeFrames([][2]int16{{10, 10}, {20, 20}}), 0x7f)
	r := newResampler(bytes.NewReader(src), 44100, 44100)

	out, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, [][2]int16{{10, 10}, {20, 20}}, decodeFrames(t, out))
}
