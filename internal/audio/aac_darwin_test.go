//go:build darwin

package audio

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/require"
)

// The testdata fixtures are 0.5 s of a 440 Hz sine at 44.1 kHz (stereo and
// mono), encoded to ADTS AAC with macOS's afconvert:
//
//	afconvert -f adts -d aac -b 96000 sine-stereo.wav sine-stereo.aac
//	afconvert -f adts -d aac -b 64000 sine-mono.wav sine-mono.aac
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

func TestAACDecodeResyncsAfterGarbage(t *testing.T) {
	garbage := append([]byte("ICY junk\xff\x00 that is not audio"), readFixture(t, "sine-stereo.aac")...)
	_, pcm := decodeAllAAC(t, bytes.NewReader(garbage))
	left, _ := stereoSamples(pcm)
	if f := dominantFreq(left, fixtureRate); f < fixtureFreq*0.9 || f > fixtureFreq*1.1 {
		t.Fatalf("dominant frequency %.1f Hz after resync, want about %.0f", f, fixtureFreq)
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
