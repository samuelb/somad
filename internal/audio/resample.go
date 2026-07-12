package audio

import (
	"encoding/binary"
	"errors"
	"io"
)

const bytesPerFrame = 4 // 16-bit little-endian PCM, 2 channels

// resampler converts a 16-bit little-endian stereo PCM stream from one sample
// rate to another using linear interpolation. It exists so streams that are
// not at the oto context's fixed rate still play at the right pitch.
type resampler struct {
	src     io.Reader
	step    float64    // source frames advanced per output frame
	pos     float64    // fractional position between frames a and b [0, 1)
	a, b    [2]float64 // adjacent source frames being interpolated
	started bool
	srcEOF  bool
}

// newResampler wraps src (16-bit LE stereo PCM at srcRate) so reads yield the
// same audio at dstRate. A malformed decoder can report a zero or negative
// rate; that falls back to a pass-through (step 1) instead of a step of 0,
// which would repeat the first frame forever — an infinite stream that never
// errors out to trigger reconnection.
func newResampler(src io.Reader, srcRate, dstRate int) *resampler {
	if srcRate <= 0 {
		srcRate = dstRate
	}
	return &resampler{
		src:  src,
		step: float64(srcRate) / float64(dstRate),
	}
}

// readFrame reads one source frame into dst. Short reads at end of stream are
// reported as io.EOF.
func (r *resampler) readFrame(dst *[2]float64) error {
	var buf [bytesPerFrame]byte
	if _, err := io.ReadFull(r.src, buf[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return io.EOF
		}
		return err
	}
	dst[0] = float64(int16(binary.LittleEndian.Uint16(buf[0:2]))) // #nosec G115 -- deliberate two's-complement decode of PCM
	dst[1] = float64(int16(binary.LittleEndian.Uint16(buf[2:4]))) // #nosec G115 -- deliberate two's-complement decode of PCM
	return nil
}

// advance moves the interpolation window forward until pos is within [0, 1).
// It returns io.EOF once the source is exhausted and fully played out.
func (r *resampler) advance() error {
	for r.pos >= 1 {
		if r.srcEOF {
			return io.EOF
		}
		r.pos--
		r.a = r.b
		if err := r.readFrame(&r.b); err != nil {
			if errors.Is(err, io.EOF) {
				// Hold the last frame so the tail interpolates flat.
				r.srcEOF = true
				r.b = r.a
				continue
			}
			return err
		}
	}
	return nil
}

func (r *resampler) Read(p []byte) (int, error) {
	if !r.started {
		if err := r.readFrame(&r.a); err != nil {
			return 0, err
		}
		if err := r.readFrame(&r.b); err != nil {
			if !errors.Is(err, io.EOF) {
				return 0, err
			}
			r.srcEOF = true
			r.b = r.a
		}
		r.started = true
	}

	n := 0
	for n+bytesPerFrame <= len(p) {
		if err := r.advance(); err != nil {
			return n, err
		}
		for ch := 0; ch < 2; ch++ {
			v := r.a[ch] + (r.b[ch]-r.a[ch])*r.pos
			binary.LittleEndian.PutUint16(p[n:], uint16(int16(clampSample(v)))) // #nosec G115 -- clamped to int16 range, deliberate encode
			n += 2
		}
		r.pos += r.step
	}
	return n, nil
}

func clampSample(v float64) float64 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return v
}
