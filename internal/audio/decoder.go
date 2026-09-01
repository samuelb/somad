package audio

import (
	"fmt"
	"io"

	mp3 "github.com/hajimehoshi/go-mp3"
)

// Stream formats, named after SomaFM's playlist format labels. The catalog
// also carries "aacp" (HE-AAC); it is never selected, because decoding it
// as plain AAC-LC would silently drop the SBR high band.
const (
	FormatMP3 = "mp3"
	FormatAAC = "aac"
)

// pcmDecoder turns an encoded stream into 16-bit little-endian stereo PCM.
type pcmDecoder interface {
	io.Reader
	SampleRate() int
}

// PreferredFormats lists the stream formats this build can decode, most
// preferred first. AAC leads where the platform decodes it: at SomaFM's
// bitrates it sounds noticeably better than MP3 (128 kbps AAC-LC vs MP3).
func PreferredFormats() []string {
	if aacSupported {
		return []string{FormatAAC, FormatMP3}
	}
	return []string{FormatMP3}
}

// newDecoder returns a decoder for the given format. Like mp3.NewDecoder,
// it blocks until enough of the stream has arrived to start decoding, so
// Play keeps its synchronous connect semantics.
func newDecoder(format string, r io.Reader) (pcmDecoder, error) {
	switch format {
	case FormatMP3:
		return mp3.NewDecoder(r)
	case FormatAAC:
		return newAACDecoder(r)
	default:
		return nil, fmt.Errorf("unsupported stream format %q", format)
	}
}
