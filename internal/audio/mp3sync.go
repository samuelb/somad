package audio

import (
	"bufio"
	"errors"
	"io"
)

// mp3MaxSkip bounds how many bytes mp3Sync may discard in search of the
// stream's first frame before it gives up with errMP3LostSync: the same
// bound, for the same reason, as the ADTS reader's adtsMaxSkip. An MP3
// frame is at most 1441 bytes, so realigning on a real stream takes less
// than that.
const mp3MaxSkip = 32 << 10

// errMP3LostSync reports that no frame header confirmed by the next frame's
// turned up within mp3MaxSkip bytes.
var errMP3LostSync = errors.New("not an MP3 stream: no valid frame header found")

const (
	mp3HeaderLen   = 4
	id3v2HeaderLen = 10
)

// mp3Sync starts an MP3 stream on a frame for go-mp3, which takes the first
// four bytes shaped like a frame header as one: an 11-bit syncword and a
// few fields, which the tail of a frame, left over from joining the stream
// between two headers, satisfies now and then. go-mp3 reads the stream's
// sample rate from that header, once, so a false one with another rate
// plays the whole session at the wrong speed and pitch, and one of a kind
// go-mp3 does not decode (layer I or II, MPEG 2.5, a free bitrate) fails
// the connect.
//
// mp3Sync discards leading bytes up to the header of a frame go-mp3
// decodes that the next frame's header, one frame length on, confirms (see
// confirmed), within mp3MaxSkip. From there on it passes the stream
// through untouched: go-mp3 finds the frames after the first on its own,
// and none of them can change the sample rate. Waiting for the second
// header delays the first audio by the four bytes that follow the first
// frame.
type mp3Sync struct {
	src    *bufio.Reader
	synced bool
	err    error // why syncing failed, returned by every Read from then on
}

func newMP3Sync(r io.Reader) *mp3Sync {
	// The buffer must hold the largest frame (1441 bytes) and the header
	// after it, for confirmed to peek at both. Once synced it stays in the
	// path: it never waits for more than the source has, and it spares the
	// pipe behind it go-mp3's many small reads.
	return &mp3Sync{src: bufio.NewReaderSize(r, 4096)}
}

func (s *mp3Sync) Read(p []byte) (int, error) {
	if !s.synced {
		if s.err == nil {
			s.err = s.sync()
		}
		if s.err != nil {
			return 0, s.err
		}
		s.synced = true
	}
	return s.src.Read(p)
}

// sync discards bytes until the stream stands at a confirmed frame header.
func (s *mp3Sync) sync() error {
	s.skipID3v2()
	for skipped := 0; ; skipped++ {
		hdr, err := s.src.Peek(mp3HeaderLen)
		if err != nil {
			return err // io.EOF at a clean stream end
		}
		if frameLen, ok := mp3FrameLen(hdr); ok && s.confirmed(frameLen) {
			return nil
		}
		if skipped == mp3MaxSkip {
			return errMP3LostSync
		}
		_, _ = s.src.Discard(1)
	}
}

// confirmed reports whether the header the stream stands at, of a frame
// frameLen bytes long, is followed by the next frame's header, for the same
// MPEG version, layer and sample rate: what a stream keeps from frame to
// frame, while bitrate, padding and stereo mode may change. Noise shaped
// like a header rarely points at a second one that matches it. A stream
// that ends or fails before the next header arrives leaves nothing to check
// against, so the candidate is taken, as adtsReader.confirmed does:
// reading on returns its frame, or the stream's end or failure.
func (s *mp3Sync) confirmed(frameLen int) bool {
	b, err := s.src.Peek(frameLen + mp3HeaderLen)
	if err != nil {
		return true
	}
	next := b[frameLen:]
	if _, ok := mp3FrameLen(next); !ok {
		return false
	}
	const versionAndLayer, sampleRate = 0x1E, 0x0C
	return b[1]&versionAndLayer == next[1]&versionAndLayer && b[2]&sampleRate == next[2]&sampleRate
}

// skipID3v2 discards an ID3v2 tag at the very start of the stream, which
// go-mp3 would skip too: a tag is not audio to search for a frame, and one
// with a picture in it is longer than the search goes on for. go-mp3 reads
// a tag into memory at whatever size its header claims, up to 256 MB; here
// it is discarded as it arrives.
func (s *mp3Sync) skipID3v2() {
	b, err := s.src.Peek(id3v2HeaderLen)
	if err != nil || string(b[:3]) != "ID3" {
		return
	}
	// Version bytes are never 0xFF, and the size is four 7-bit bytes, so
	// that no tag header contains a syncword. Anything else is not a tag.
	if b[3] == 0xFF || b[4] == 0xFF || (b[6]|b[7]|b[8]|b[9])&0x80 != 0 {
		return
	}
	size := int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
	// A stream that ends inside the tag fails the search's first read.
	_, _ = s.src.Discard(id3v2HeaderLen + size)
}

// MPEG audio layer III bitrates in kbit/s by the header's bitrate index,
// for MPEG-1 and for MPEG-2. Index 0 is the free format, where the header
// does not say how long the frame is; 15 is invalid.
var (
	mp3BitratesV1 = [...]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	mp3BitratesV2 = [...]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
)

// mp3SampleRates maps the header's sampling frequency index to Hz for
// MPEG-1; MPEG-2 halves them. Index 3 is reserved.
var mp3SampleRates = [...]int{44100, 48000, 32000}

// mp3FrameLen returns the length in bytes of the frame that the four header
// bytes at the start of b describe, and whether they are the header of a
// frame go-mp3 decodes: MPEG-1 or MPEG-2 layer III at a bitrate the header
// names. It is stricter than go-mp3's own validity check in every respect,
// so a header accepted here is the first one go-mp3 accepts.
func mp3FrameLen(b []byte) (int, bool) {
	if b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return 0, false
	}
	const (
		mpeg2    = 2 // version bits; 0 is MPEG 2.5 and 1 reserved
		mpeg1    = 3
		layerIII = 1 // layer bits
	)
	version := b[1] >> 3 & 0x3
	bitrate := int(b[2] >> 4)
	rate := int(b[2] >> 2 & 0x3)
	padding := int(b[2] >> 1 & 0x1)
	if version < mpeg2 || b[1]>>1&0x3 != layerIII ||
		bitrate == 0 || bitrate == 15 || rate == len(mp3SampleRates) ||
		b[3]&0x3 == 2 { // a reserved emphasis, which go-mp3 does not take for a header
		return 0, false
	}
	if version == mpeg1 {
		return 144000*mp3BitratesV1[bitrate]/mp3SampleRates[rate] + padding, true
	}
	// An MPEG-2 frame holds half the samples of an MPEG-1 one.
	return 72000*mp3BitratesV2[bitrate]/(mp3SampleRates[rate]/2) + padding, true
}
