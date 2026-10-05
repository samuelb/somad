package audio

import (
	"encoding/binary"
	"io"
	"math"
	"math/cmplx"
	"sync"
	"time"
)

// Spectrum analysis for the TUI's background visualizer (ADR-0034): a tap
// on the decoded PCM the oto player pulls keeps the newest samples, and
// Spectrum runs an FFT over the window that is audible right now.

const (
	// fftSize is the analysis window: about 93 ms at 44.1 kHz, enough
	// resolution (10.8 Hz per bin) to tell bass bands apart.
	fftSize = 4096
	// tapFrames is how much audio the tap keeps: about 1.5 s, room for
	// oto's half-second player buffer, the device's own, and a window.
	tapFrames = 1 << 16
	// tapStallAfter is how long the tap may take no audio before Spectrum
	// reports silence: oto tops its buffer up every few tens of
	// milliseconds while the stream flows, so this means a stalled stream,
	// whose last window would otherwise hold the bars up until it recovers.
	tapStallAfter = 250 * time.Millisecond
	// deviceLatency estimates how far the device plays behind what oto
	// has taken from its player's buffer (on macOS, four AudioQueue
	// buffers of 1536 frames); added to the player's BufferedSize.
	deviceLatency = 140 * time.Millisecond
)

// The frequency range Bands spreads its bands over, logarithmically, and
// the per-octave power range it maps onto 0–255, in dB relative to a full-
// scale sine. Measured on SomaFM channels from Drone Zone to DEF CON, a
// band spends most of its time between -40 and -20 dB, so the range is
// narrow enough that the bars move, and loud peaks touch the top. The top
// band stops short of where 128k MP3 cuts the treble off.
const (
	bandLowHz  = 50.0
	bandHighHz = 12000.0
	floorDB    = -45.0
	ceilingDB  = -12.0
)

// pcmTap passes the decoded stream through to the oto player, recording
// the newest frames, mixed to mono, for Spectrum. oto reads on its own
// goroutine and Spectrum runs on the caller's, so the ring is locked.
type pcmTap struct {
	r io.Reader

	mu       sync.Mutex
	ring     []float32 // mono samples in [-1, 1], tapFrames long
	written  uint64    // frames recorded in total; the newest is at written-1
	lastRead time.Time // when record last took audio
	partial  [bytesPerFrame]byte
	npartial int // bytes of a frame split across reads, held in partial
}

func newPCMTap(r io.Reader) *pcmTap {
	return &pcmTap{r: r, ring: make([]float32, tapFrames)}
}

func (t *pcmTap) Read(b []byte) (int, error) {
	n, err := t.r.Read(b)
	if n > 0 {
		t.record(b[:n])
	}
	return n, err
}

// record appends the frames in b to the ring, carrying a frame split
// across reads over to the next one.
func (t *pcmTap) record(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastRead = time.Now()
	if t.npartial > 0 {
		k := copy(t.partial[t.npartial:], b)
		t.npartial += k
		b = b[k:]
		if t.npartial < bytesPerFrame {
			return
		}
		t.push(t.partial[:])
		t.npartial = 0
	}
	whole := len(b) / bytesPerFrame * bytesPerFrame
	for i := 0; i < whole; i += bytesPerFrame {
		t.push(b[i : i+bytesPerFrame])
	}
	t.npartial = copy(t.partial[:], b[whole:])
}

// push records one frame. Caller holds t.mu.
func (t *pcmTap) push(frame []byte) {
	l := int16(binary.LittleEndian.Uint16(frame))     //nolint:gosec // reinterpreting the sample's bits is the point
	r := int16(binary.LittleEndian.Uint16(frame[2:])) //nolint:gosec // as above
	t.ring[t.written%tapFrames] = (float32(l) + float32(r)) / (2 * 32768)
	t.written++
}

// stalled reports whether the tap has taken no audio for tapStallAfter.
func (t *pcmTap) stalled() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Since(t.lastRead) > tapStallAfter
}

// window fills dst with the frames that end delay frames before the newest
// one, reporting false while the tap holds too few.
func (t *pcmTap) window(dst []float32, delay int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	delay = min(max(delay, 0), tapFrames-len(dst))
	need := uint64(delay + len(dst)) //nolint:gosec // both are non-negative and small
	if t.written < need {
		return false
	}
	start := t.written - need
	for i := range dst {
		dst[i] = t.ring[(start+uint64(i))%tapFrames] //nolint:gosec // i is non-negative
	}
	return true
}

// Spectrum is the power spectrum of the audio playing at one moment: the
// power in each FFT bin from DC up to the Nyquist frequency, where a full-
// scale sine is 1. The zero value is silence.
type Spectrum struct {
	power []float64
}

// Spectrum analyzes the audio of the current session that is audible now:
// the tap runs ahead of the speakers by what oto has buffered, so the
// window ends that far back. It reports false while nothing is playing,
// the stream stalled included.
func (p *AudioPlayer) Spectrum() (Spectrum, bool) {
	p.mu.Lock()
	s := p.current
	p.mu.Unlock()
	if s == nil || s.tap == nil || s.tap.stalled() {
		return Spectrum{}, false
	}
	// BufferedSize only reads oto's buffer under oto's own lock, so it is
	// safe off the session goroutine that otherwise owns the player.
	delay := s.player.BufferedSize()/bytesPerFrame + int(deviceLatency*sampleRate/time.Second)
	samples := make([]float32, fftSize)
	if !s.tap.window(samples, delay) {
		return Spectrum{}, false
	}
	return analyze(samples), true
}

// hannWindow tapers the analysis window so a tone's energy stays in its
// bin instead of leaking across the spectrum.
var hannWindow = func() []float64 {
	w := make([]float64, fftSize)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/fftSize)
	}
	return w
}()

// analyze returns the power spectrum of fftSize samples.
func analyze(samples []float32) Spectrum {
	x := make([]complex128, len(samples))
	for i, v := range samples {
		x[i] = complex(float64(v)*hannWindow[i], 0)
	}
	fft(x)
	// A sine of amplitude A peaks at A·N/4 in its bin under a Hann window
	// (coherent gain ½, half the energy in the negative frequencies).
	norm := float64(len(x)) / 4
	power := make([]float64, len(x)/2+1)
	for k := range power {
		m := cmplx.Abs(x[k]) / norm
		power[k] = m * m
	}
	return Spectrum{power: power}
}

// fft transforms x in place: an iterative radix-2 Cooley–Tukey FFT, so
// len(x) must be a power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := range size / 2 {
				a, b := x[start+k], x[start+k+size/2]*w
				x[start+k], x[start+k+size/2] = a+b, a-b
				w *= step
			}
		}
	}
}

// Bands sums the spectrum into n bands spread logarithmically from
// bandLowHz to bandHighHz, lowest first, and maps each band's power per
// octave from floorDB–ceilingDB onto 0–255. Per octave, so the levels do
// not depend on n; summed rather than averaged, so music, whose power
// falls with frequency about as fast as the bands widen, comes out level
// across the range. A bin's power counts toward a band by how much of the
// bin it covers, so a bass band narrower than a bin gets its share.
func (s Spectrum) Bands(n int) []byte {
	out := make([]byte, n)
	if len(s.power) == 0 || n <= 0 {
		return out
	}
	binHz := float64(sampleRate) / float64(2*(len(s.power)-1))
	octaves := math.Log2(bandHighHz / bandLowHz)
	for i := range out {
		lo := bandLowHz * math.Pow(2, octaves*float64(i)/float64(n)) / binHz
		hi := bandLowHz * math.Pow(2, octaves*float64(i+1)/float64(n)) / binHz
		var sum float64
		for k := int(lo + 0.5); k < len(s.power) && float64(k)-0.5 < hi; k++ {
			overlap := math.Min(hi, float64(k)+0.5) - math.Max(lo, float64(k)-0.5)
			if overlap > 0 {
				sum += s.power[k] * overlap
			}
		}
		db := 10 * math.Log10(sum*float64(n)/octaves+1e-12)
		level := (db - floorDB) / (ceilingDB - floorDB)
		out[i] = byte(math.Round(255 * min(max(level, 0), 1)))
	}
	return out
}
