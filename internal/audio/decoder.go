package audio

import (
	"fmt"
	"io"

	mp3 "github.com/hajimehoshi/go-mp3"
)

// Stream formats, named after SomaFM's playlist format labels. "aac" is a
// channel's 128 kbps AAC stream (AAC-LC or HE-AAC, depending on the
// channel); "aacp" is HE-AAC, the only format SomaFM offers at its lower
// "high" and "low" qualities. Both decode through the same AAC decoder,
// which tells the variants apart from the stream itself.
const (
	FormatMP3  = "mp3"
	FormatAAC  = "aac"
	FormatAACP = "aacp"
)

// pcmDecoder turns an encoded stream into 16-bit little-endian stereo PCM.
type pcmDecoder interface {
	io.Reader
	SampleRate() int
}

// PreferredFormats lists the stream formats this build can decode, most
// preferred first. AAC leads where the platform decodes it: at SomaFM's
// bitrates it sounds noticeably better than MP3. HE-AAC ranks next, ahead
// of MP3; format order only breaks ties, though, since the quality
// preference comes first (channels.SelectPlaylists), which is how "high"
// and "low" pick the HE-AAC playlists. Without AAC decoding the setting
// has nothing to choose from: SomaFM offers MP3 at "highest" only.
func PreferredFormats() []string {
	if aacSupported {
		return []string{FormatAAC, FormatAACP, FormatMP3}
	}
	return []string{FormatMP3}
}

// newDecoder returns a decoder for the given format. Like mp3.NewDecoder,
// it blocks until enough of the stream has arrived to start decoding, so
// Play keeps its synchronous connect semantics. A variable so tests can
// substitute a decoder that fails mid-stream.
var newDecoder = func(format string, r io.Reader) (pcmDecoder, error) {
	switch format {
	case FormatMP3:
		return mp3.NewDecoder(r)
	case FormatAAC, FormatAACP:
		return newAACDecoder(r)
	default:
		return nil, fmt.Errorf("unsupported stream format %q", format)
	}
}
