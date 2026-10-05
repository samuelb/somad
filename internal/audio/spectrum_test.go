package audio

import (
	"encoding/binary"
	"io"
	"math"
	"math/cmplx"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pcm encodes mono samples in [-1, 1] as 16-bit stereo frames, both
// channels alike.
func pcm(samples []float64) []byte {
	b := make([]byte, 0, len(samples)*bytesPerFrame)
	for _, s := range samples {
		v := uint16(int16(math.Round(s * 32767))) //nolint:gosec // in range by construction
		b = binary.LittleEndian.AppendUint16(b, v)
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return b
}

// sine returns n samples of a full-scale sine at hz.
func sine(hz float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = math.Sin(2 * math.Pi * hz * float64(i) / sampleRate)
	}
	return s
}

func toFloat32(s []float64) []float32 {
	out := make([]float32, len(s))
	for i, v := range s {
		out[i] = float32(v)
	}
	return out
}

func TestFFT_MatchesDFT(t *testing.T) {
	const n = 64
	x := make([]complex128, n)
	for i := range x {
		x[i] = complex(rand.Float64()*2-1, rand.Float64()*2-1) //nolint:gosec // test data
	}
	want := make([]complex128, n)
	for k := range want {
		for j, v := range x {
			want[k] += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*j)/n))
		}
	}
	fft(x)
	for k := range x {
		assert.InDelta(t, real(want[k]), real(x[k]), 1e-9, "bin %d real", k)
		assert.InDelta(t, imag(want[k]), imag(x[k]), 1e-9, "bin %d imag", k)
	}
}

func TestAnalyze_FullScaleSinePeaksAtOne(t *testing.T) {
	// A tone on a bin center: its power lands in that bin, at 1.
	bin := 93
	hz := float64(bin) * sampleRate / fftSize
	spec := analyze(toFloat32(sine(hz, fftSize)))
	peak := slices.Index(spec.power, slices.Max(spec.power))
	assert.Equal(t, bin, peak)
	assert.InDelta(t, 1, spec.power[bin], 0.01)
}

func TestBands_ToneLightsItsBand(t *testing.T) {
	const n = 24
	levels := analyze(toFloat32(sine(1000, fftSize))).Bands(n)
	require.Len(t, levels, n)
	octaves := math.Log2(bandHighHz / bandLowHz)
	want := int(math.Log2(1000/bandLowHz) / octaves * n)
	assert.Equal(t, want, slices.Index(levels, slices.Max(levels)), "levels %v", levels)
	assert.EqualValues(t, 255, levels[want], "a full-scale tone tops out its band")
	assert.Zero(t, levels[0], "the bass stays dark")
	assert.Zero(t, levels[n-1], "and so does the treble")
}

func TestBands_LevelsDoNotDependOnTheBandCount(t *testing.T) {
	// Pink-ish noise: the same signal in 12 or 48 bands reads alike.
	samples := make([]float32, fftSize)
	var b0, b1, b2 float64
	for i := range samples {
		w := rand.Float64()*2 - 1 //nolint:gosec // test data
		b0 = 0.99765*b0 + w*0.0990460
		b1 = 0.96300*b1 + w*0.2965164
		b2 = 0.57000*b2 + w*1.0526913
		samples[i] = float32((b0 + b1 + b2 + w*0.1848) * 0.05)
	}
	spec := analyze(samples)
	mean := func(b []byte) float64 {
		var sum float64
		for _, v := range b {
			sum += float64(v)
		}
		return sum / float64(len(b))
	}
	assert.InDelta(t, mean(spec.Bands(12)), mean(spec.Bands(48)), 12)
}

func TestBands_SilenceAndZeroValue(t *testing.T) {
	assert.Equal(t, make([]byte, 8), analyze(make([]float32, fftSize)).Bands(8))
	assert.Equal(t, make([]byte, 8), Spectrum{}.Bands(8))
	assert.Empty(t, Spectrum{}.Bands(0))
}

func TestPCMTap_RecordsFramesSplitAcrossReads(t *testing.T) {
	samples := []float64{0.5, -0.25, 1, -1, 0.125}
	data := pcm(samples)
	tap := newPCMTap(nil)
	// Odd chunk sizes split frames, and even a sample, between reads.
	for _, chunk := range [][]byte{data[:3], data[3:6], data[6:7], data[7:]} {
		tap.record(chunk)
	}
	got := make([]float32, len(samples))
	require.True(t, tap.window(got, 0))
	for i, want := range samples {
		assert.InDelta(t, want, got[i], 1e-4, "frame %d", i)
	}
	assert.False(t, tap.window(make([]float32, len(samples)+1), 0), "more than was recorded")
}

func TestPCMTap_WindowEndsDelayFramesBack(t *testing.T) {
	tap := newPCMTap(nil)
	ramp := make([]float64, 100)
	for i := range ramp {
		ramp[i] = float64(i) / 128
	}
	tap.record(pcm(ramp))
	got := make([]float32, 3)
	require.True(t, tap.window(got, 10))
	assert.InDeltaSlice(t, []float32{87.0 / 128, 88.0 / 128, 89.0 / 128}, got, 1e-4)
	assert.False(t, tap.window(got, 98))
}

func TestPCMTap_WrapsAround(t *testing.T) {
	tap := newPCMTap(nil)
	tap.record(pcm(make([]float64, tapFrames-1)))
	tap.record(pcm([]float64{0.5, 0.25}))
	got := make([]float32, 2)
	require.True(t, tap.window(got, 0))
	assert.InDeltaSlice(t, []float32{0.5, 0.25}, got, 1e-4)
}

func TestPCMTap_PassesTheStreamThrough(t *testing.T) {
	data := pcm(sine(440, 64))
	tap := newPCMTap(&chunkReader{data: data, chunk: 7})
	var out []byte
	buf := make([]byte, 16)
	for {
		n, err := tap.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	assert.Equal(t, data, out)
	assert.EqualValues(t, 64, tap.written)
}

// chunkReader returns data at most chunk bytes per read.
type chunkReader struct {
	data  []byte
	chunk int
}

func (r *chunkReader) Read(b []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(b[:min(len(b), r.chunk)], r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestAudioPlayerSpectrum(t *testing.T) {
	p := newTestPlayer()
	_, ok := p.Spectrum()
	assert.False(t, ok, "nothing plays")

	tap := newPCMTap(nil)
	p.current = &session{player: &fakeOutputPlayer{}, tap: tap}
	_, ok = p.Spectrum()
	assert.False(t, ok, "the tap has not filled yet")

	// A tone, then silence for as long as the device lags: the window
	// that is audible now holds the tone.
	lag := int(deviceLatency * sampleRate / time.Second)
	tap.record(pcm(sine(1000, fftSize)))
	tap.record(pcm(make([]float64, lag)))
	spec, ok := p.Spectrum()
	require.True(t, ok)
	assert.NotZero(t, slices.Max(spec.Bands(16)))

	// A stream that stalls stops holding the bars up.
	tap.mu.Lock()
	tap.lastRead = time.Now().Add(-2 * tapStallAfter)
	tap.mu.Unlock()
	_, ok = p.Spectrum()
	assert.False(t, ok, "stalled")
}
