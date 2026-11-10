//go:build darwin

package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The testdata fixtures are 0.5 s of a 440 Hz sine at 44.1 kHz (stereo and
// mono), encoded to ADTS AAC with macOS's afconvert:
//
//	afconvert -f adts -d aac -b 96000 sine-stereo.wav sine-stereo.aac
//	afconvert -f adts -d aac -b 64000 sine-mono.wav sine-mono.aac
//
// plus two HE-AAC fixtures shaped like SomaFM's "aacp" streams (a 22.05 kHz
// AAC-LC core in the ADTS header, SBR and parametric stereo only in the
// payload), each 0.5 s at 44.1 kHz: a 440 Hz sine with a quieter 15 kHz
// sine on both channels, which only the SBR band can carry, and a 440 Hz
// sine on the left channel alone, which only parametric stereo can place:
//
//	afconvert -f adts -d aach -b 64000 he-aac-stereo.wav he-aac-stereo.aac
//	afconvert -f adts -d aacp -b 32000 he-aac-v2-left.wav he-aac-v2-left.aac
//
// and the same two signals 4 s long (the 440 Hz sine at 0.6 of full scale,
// the 15 kHz one at 0.2), long enough to hold the SBR and parametric-stereo
// headers the encoder repeats every 21 frames, about a second, for
// TestAACDecodeJoinedMidStream:
//
//	afconvert -f adts -d aach -b 64000 he-aac-stereo-4s.wav he-aac-stereo-4s.aac
//	afconvert -f adts -d aacp -b 32000 he-aac-v2-left-4s.wav he-aac-v2-left-4s.aac
const (
	fixtureRate    = 44100
	fixtureSeconds = 0.5
	fixtureFreq    = 440.0
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name)) // #nosec G304 -- fixed fixture names within the test
	require.NoError(t, err)
	return data
}

func decodeAllAAC(t *testing.T, r io.Reader) (*aacDecoder, []byte) {
	t.Helper()
	dec, err := newAACDecoder(r)
	require.NoError(t, err)
	pcm, err := io.ReadAll(dec)
	require.NoError(t, err)
	return dec.(*aacDecoder), pcm
}

// stereoSamples interprets stereo s16le PCM as per-channel int16 slices.
func stereoSamples(pcm []byte) (left, right []int16) {
	for i := 0; i+3 < len(pcm); i += 4 {
		left = append(left, int16(binary.LittleEndian.Uint16(pcm[i:])))     // #nosec G115 -- reinterpreting the sample's bits
		right = append(right, int16(binary.LittleEndian.Uint16(pcm[i+2:]))) // #nosec G115 -- reinterpreting the sample's bits
	}
	return left, right
}

// dominantFreq estimates the tone frequency from zero crossings, skipping
// the encoder's priming samples at the start.
func dominantFreq(ch []int16, rate int) float64 {
	const skip = 4096
	if len(ch) <= skip {
		return 0
	}
	ch = ch[skip:]
	crossings := 0
	for i := 1; i < len(ch); i++ {
		if (ch[i-1] < 0) != (ch[i] < 0) {
			crossings++
		}
	}
	return float64(crossings) / 2 / (float64(len(ch)) / float64(rate))
}

func TestAACDecodeStereoSine(t *testing.T) {
	dec, pcm := decodeAllAAC(t, bytes.NewReader(readFixture(t, "sine-stereo.aac")))
	require.Equal(t, fixtureRate, dec.SampleRate())

	frames := len(pcm) / 4
	want := int(fixtureSeconds * fixtureRate)
	// AAC pads with priming/remainder frames; allow a generous margin.
	if frames < want*8/10 || frames > want*13/10 {
		t.Fatalf("decoded %d frames, want about %d", frames, want)
	}

	left, _ := stereoSamples(pcm)
	peak := int16(0)
	for _, s := range left {
		if s > peak {
			peak = s
		}
	}
	if peak < 8000 {
		t.Fatalf("peak amplitude %d, want a clearly audible sine (>8000)", peak)
	}

	if f := dominantFreq(left, fixtureRate); f < fixtureFreq*0.9 || f > fixtureFreq*1.1 {
		t.Fatalf("dominant frequency %.1f Hz, want about %.0f", f, fixtureFreq)
	}
}

func TestAACDecodeMonoDuplicatesChannels(t *testing.T) {
	_, pcm := decodeAllAAC(t, bytes.NewReader(readFixture(t, "sine-mono.aac")))
	left, right := stereoSamples(pcm)
	require.NotEmpty(t, left)
	for i := range left {
		if left[i] != right[i] {
			t.Fatalf("sample %d: left %d != right %d", i, left[i], right[i])
		}
	}
	if f := dominantFreq(left, fixtureRate); f < fixtureFreq*0.9 || f > fixtureFreq*1.1 {
		t.Fatalf("dominant frequency %.1f Hz, want about %.0f", f, fixtureFreq)
	}
}

// highBandFraction returns the share of ch's power above lo Hz, from a
// Hann-windowed DFT (the visualizer's FFT) of one block past the encoder's
// priming samples.
func highBandFraction(ch []int16, rate int, lo float64) float64 {
	const skip, n = 4096, 2048
	if len(ch) < skip+n {
		return 0
	}
	block := make([]complex128, n)
	for i := range block {
		block[i] = complex(float64(ch[skip+i])*(0.5-0.5*math.Cos(2*math.Pi*float64(i)/n)), 0)
	}
	fft(block)
	var total, high float64
	for k := 1; k < n/2; k++ {
		p := real(block[k])*real(block[k]) + imag(block[k])*imag(block[k])
		total += p
		if float64(k)*float64(rate)/n > lo {
			high += p
		}
	}
	return high / total
}

// tonePower returns the mean power of ch at freq (Goertzel), skipping the
// encoder's priming samples at the start.
func tonePower(ch []int16, rate int, freq float64) float64 {
	const skip = 4096
	if len(ch) <= skip {
		return 0
	}
	ch = ch[skip:]
	k := 2 * math.Cos(2*math.Pi*freq/float64(rate))
	var s1, s2 float64
	for _, x := range ch {
		s1, s2 = float64(x)+k*s1-s2, s1
	}
	n := float64(len(ch))
	return (s1*s1 + s2*s2 - k*s1*s2) / (n * n)
}

func TestAACDecodeHEAACRestoresTheSBRBand(t *testing.T) {
	dec, pcm := decodeAllAAC(t, bytes.NewReader(readFixture(t, "he-aac-stereo.aac")))

	// The ADTS header says 22.05 kHz; SBR doubles it. Decoding the core
	// alone would cap the audio at 11 kHz and lose the 15 kHz tone.
	require.Equal(t, fixtureRate, dec.SampleRate())
	left, right := stereoSamples(pcm)
	frames := len(left)
	want := int(fixtureSeconds * fixtureRate)
	if frames < want*8/10 || frames > want*13/10 {
		t.Fatalf("decoded %d frames, want about %d", frames, want)
	}
	for name, ch := range map[string][]int16{"left": left, "right": right} {
		// The 15 kHz tone carries a ninth of the input's power; SBR
		// rebuilds it from a coarse envelope, so only the band is checked.
		if frac := highBandFraction(ch, fixtureRate, 11500); frac < 0.05 {
			t.Fatalf("%s: %.2f%% of the power above 11.5 kHz, want about 11%%; the SBR band is missing",
				name, 100*frac)
		}
	}
}

func TestAACDecodeHEAACv2RestoresParametricStereo(t *testing.T) {
	dec, pcm := decodeAllAAC(t, bytes.NewReader(readFixture(t, "he-aac-v2-left.aac")))

	// A mono core in the header; parametric stereo puts the tone back on
	// the left. Decoding the core alone would duplicate it onto both sides.
	require.Equal(t, fixtureRate, dec.SampleRate())
	left, right := stereoSamples(pcm)
	if f := dominantFreq(left, fixtureRate); f < fixtureFreq*0.9 || f > fixtureFreq*1.1 {
		t.Fatalf("left: dominant frequency %.1f Hz, want about %.0f", f, fixtureFreq)
	}
	l, r := tonePower(left, fixtureRate, fixtureFreq), tonePower(right, fixtureRate, fixtureFreq)
	if r > l/10 {
		t.Fatalf("right channel carries the left-only tone at %.1f dB below the left; want a stereo image",
			10*math.Log10(l/r))
	}
}

// adtsFrames splits an ADTS stream into its frames, headers included.
func adtsFrames(t *testing.T, stream []byte) [][]byte {
	t.Helper()
	r := newADTSReader(bytes.NewReader(stream))
	var frames [][]byte
	for {
		f, err := r.next()
		if errors.Is(err, io.EOF) {
			return frames
		}
		require.NoError(t, err)
		frames = append(frames, f.raw)
	}
}

func TestAACDecodeJoinedMidStream(t *testing.T) {
	// A live stream is always joined somewhere in its middle, and the
	// encoder repeats the SBR and parametric-stereo headers only now and
	// then, so the first frames a listener gets usually carry neither. The
	// probe must name the full format from such frames all the same: the
	// converter is built once, and one built for the AAC-LC core alone
	// plays band-limited, or in mono, for the rest of the session. The
	// converter then has to lock on once the headers come round: until
	// then it has only the core to play.
	//
	// joinSpan is more than the 21 frames from one header to the next, so
	// the stream is joined at every distance from a header.
	const joinSpan = 22
	// The checks below look at the end of the output, some 60 frames after
	// the last join: on macOS 27 the SBR band is back at the first header
	// after the join and the stereo image by the second. The helpers skip
	// 4096 samples for an encoder's priming and measure what follows.
	const tail = 8192

	sbrBand := func(t *testing.T, join int, left, right []int16) {
		for name, ch := range map[string][]int16{"left": left, "right": right} {
			if frac := highBandFraction(ch, fixtureRate, 11500); frac < 0.05 {
				t.Fatalf("joined at frame %d, %s: %.2f%% of the power above 11.5 kHz at the end, want about 10%%; the SBR band never came back",
					join, name, 100*frac)
			}
		}
	}
	stereoImage := func(t *testing.T, join int, left, right []int16) {
		if f := dominantFreq(left, fixtureRate); f < fixtureFreq*0.9 || f > fixtureFreq*1.1 {
			t.Fatalf("joined at frame %d, left: dominant frequency %.1f Hz at the end, want about %.0f", join, f, fixtureFreq)
		}
		l, r := tonePower(left, fixtureRate, fixtureFreq), tonePower(right, fixtureRate, fixtureFreq)
		if r > l/10 {
			t.Fatalf("joined at frame %d: the right channel carries the left-only tone at %.1f dB below the left at the end; the stereo image never came back",
				join, 10*math.Log10(l/r))
		}
	}

	for _, tc := range []struct {
		fixture string
		// lockedOn checks the end of the output; nil for the half-second
		// fixtures, which end before another header comes.
		lockedOn func(t *testing.T, join int, left, right []int16)
	}{
		{"he-aac-stereo.aac", nil},
		{"he-aac-v2-left.aac", nil},
		{"he-aac-stereo-4s.aac", sbrBand},
		{"he-aac-v2-left-4s.aac", stereoImage},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			frames := adtsFrames(t, readFixture(t, tc.fixture))
			// Leave the probe as many frames as it may ask for.
			last := min(joinSpan, len(frames)-aacProbeFrames)
			require.Positive(t, last, "the fixture is too short to join mid-stream")
			for join := 0; join <= last; join++ {
				dec, err := newAACDecoder(bytes.NewReader(bytes.Join(frames[join:], nil)))
				require.NoError(t, err, "joined at frame %d", join)
				// The headers say 22.05 kHz, and mono for HE-AAC v2.
				require.Equal(t, fixtureRate, dec.SampleRate(), "joined at frame %d", join)
				pcm, err := io.ReadAll(dec)
				require.NoError(t, err, "joined at frame %d", join)
				left, right := stereoSamples(pcm)
				require.Greater(t, len(left), tail, "joined at frame %d", join)
				if tc.lockedOn != nil {
					tc.lockedOn(t, join, left[len(left)-tail:], right[len(right)-tail:])
				}
			}
		})
	}
}

func TestNewDecoder_HEAACFormatIsDecoded(t *testing.T) {
	// SomaFM's "aacp" playlists (its "high" and "low" qualities) are HE-AAC,
	// decoded by the same decoder as "aac".
	assert.Contains(t, PreferredFormats(), FormatAACP)
	dec, err := newDecoder(FormatAACP, bytes.NewReader(readFixture(t, "he-aac-v2-left.aac")))
	require.NoError(t, err)
	assert.Equal(t, fixtureRate, dec.SampleRate())
}

// cafHeader returns the header of a Core Audio Format file holding HE-AAC
// v2 with the given magic cookie: the AAC-LC core it is stored as, the
// cookie, and the list of formats it decodes as, richest first.
func cafHeader(cookie []byte) []byte {
	// A CAF audio description: sample rate, format, flags, bytes per
	// packet, frames per packet, channels, bits per channel; big-endian.
	desc := func(rate float64, format string, framesPerPacket, channels uint32) []byte {
		b := binary.BigEndian.AppendUint64(nil, math.Float64bits(rate))
		b = append(b, format...)
		for _, v := range []uint32{0, 0, framesPerPacket, channels, 0} {
			b = binary.BigEndian.AppendUint32(b, v)
		}
		return b
	}
	chunk := func(b []byte, kind string, body []byte) []byte {
		b = append(b, kind...)
		b = binary.BigEndian.AppendUint64(b, uint64(len(body)))
		return append(b, body...)
	}
	const stereoLayout = 101<<16 | 2 // kAudioChannelLayoutTag_Stereo
	formats := binary.BigEndian.AppendUint32(desc(44100, "aacp", 2048, 2), stereoLayout)

	b := []byte("caff\x00\x01\x00\x00") // version 1, no flags
	b = chunk(b, "desc", desc(22050, "aac ", 1024, 1))
	b = chunk(b, "kuki", cookie)
	return chunk(b, "ldsc", formats)
}

func TestAACProbeKeepsALargeMagicCookie(t *testing.T) {
	// SomaFM's cookies are 39 to 44 bytes, and ADTS cannot describe a
	// format whose cookie would be much larger. A CAF file's cookie can be
	// any size, and the system parser recognizes the file despite the ADTS
	// hint, so it stands in for a stream with a cookie of a kilobyte, which
	// must reach the converter whole rather than not at all.
	cookie := bytes.Repeat([]byte{0x5A}, 1024)
	d := &aacDecoder{
		frames: newADTSReader(bytes.NewReader(nil)),
		queued: []adtsFrame{{raw: cafHeader(cookie)}},
	}
	format, err := d.probe()
	require.NoError(t, err)
	assert.EqualValues(t, 44100, format.asbd.mSampleRate)
	require.Len(t, format.cookie, len(cookie))
	assert.True(t, bytes.Equal(cookie, format.cookie), "the cookie came back altered")
}

func TestAACDecodeResyncsAfterGarbage(t *testing.T) {
	garbage := append([]byte("ICY junk\xff\x00 that is not audio"), readFixture(t, "sine-stereo.aac")...)
	_, pcm := decodeAllAAC(t, bytes.NewReader(garbage))
	left, _ := stereoSamples(pcm)
	if f := dominantFreq(left, fixtureRate); f < fixtureFreq*0.9 || f > fixtureFreq*1.1 {
		t.Fatalf("dominant frequency %.1f Hz after resync, want about %.0f", f, fixtureFreq)
	}
}

func TestAACDecodeRejectsAFormatChangeMidStream(t *testing.T) {
	// The converter is built for the first frame's core rate and channel
	// count. A stream switching either one mid-way (here, another encoding
	// spliced on at a frame boundary) would be decoded as the wrong format,
	// so it fails with an error that leads to a reconnect, which probes
	// the stream afresh.
	for _, tc := range []struct {
		name, next, change string
	}{
		{"channels", "sine-mono.aac", "(44100 Hz, 2 ch -> 44100 Hz, 1 ch)"},
		{"sample rate", "he-aac-stereo.aac", "(44100 Hz, 2 ch -> 22050 Hz, 2 ch)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := append(readFixture(t, "sine-stereo.aac"), readFixture(t, tc.next)...)
			dec, err := newAACDecoder(bytes.NewReader(stream))
			require.NoError(t, err)
			pcm, err := io.ReadAll(dec)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "AAC stream parameters changed mid-stream "+tc.change)
			assert.NotEmpty(t, pcm, "the frames before the change still decode")
		})
	}
}

func TestAACDecodeTruncatedStream(t *testing.T) {
	full := readFixture(t, "sine-stereo.aac")
	dec, err := newAACDecoder(bytes.NewReader(full[:len(full)/3]))
	require.NoError(t, err)
	// Must terminate with an error (EOF family), not hang or panic.
	if _, err := io.ReadAll(dec); err == nil {
		t.Fatal("decoding a truncated stream succeeded; want an error")
	}
}

func TestAACDecoderRejectsEmptyInput(t *testing.T) {
	if _, err := newAACDecoder(bytes.NewReader(nil)); err == nil {
		t.Fatal("newAACDecoder on empty input succeeded; want an error")
	}
}

func TestAACDecoderRejectsNonADTSStream(t *testing.T) {
	// A stream that is not ADTS at all (an HTML error page, an MP3 stream)
	// must fail construction — this is what lets the server fall back to
	// the MP3 candidate. (A stream with valid ADTS framing but corrupt
	// payloads is out of reach: AudioToolbox conceals bad payloads instead
	// of erroring.)
	notAAC := bytes.Repeat([]byte("<html>service unavailable</html>\n"), 64)
	if _, err := newAACDecoder(bytes.NewReader(notAAC)); err == nil {
		t.Fatal("newAACDecoder on a non-ADTS stream succeeded; want an error")
	}
}

func TestPlay_AACStream(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	securitytest.AllowTestHosts(t)
	t.Cleanup(func() { stopNext(p) })

	// Stream the AAC fixture and then hold the connection open, like a live
	// Icecast server that has sent its initial burst.
	fixture := readFixture(t, "sine-stereo.aac")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fixture)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	require.NoError(t, playNext(p, server.URL, FormatAAC))
	require.EqualValues(t, 1, ctx.players.Load())
	stopNext(p)
}
