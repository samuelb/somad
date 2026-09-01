package audio

import (
	"bufio"
	"errors"
	"io"
)

// adtsSampleRates maps the ADTS sampling_frequency_index to Hz.
var adtsSampleRates = [...]int{
	96000, 88200, 64000, 48000, 44100, 32000, 24000,
	22050, 16000, 12000, 11025, 8000, 7350,
}

// errADTSMultipleBlocks reports an ADTS frame carrying more than one raw
// data block — legal in the spec but unheard of on streaming servers, and
// unsupported by the one-packet-per-frame decoding here. A hard error (not
// a resync) so such a stream fails the connect and playback falls back to
// the channel's MP3 stream.
var errADTSMultipleBlocks = errors.New("unsupported ADTS stream: multiple raw data blocks per frame")

// adtsFrame is one AAC access unit extracted from an ADTS stream.
type adtsFrame struct {
	sampleRate int
	channels   int
	payload    []byte // raw AAC frame, header and CRC stripped
}

// adtsReader extracts AAC frames from an ADTS bitstream (the framing
// Shoutcast/Icecast AAC streams use). It resynchronizes on the syncword, so
// joining mid-stream or stray garbage only costs the bytes up to the next
// valid header.
type adtsReader struct {
	src *bufio.Reader
	// aligned is true at the stream start and right after a cleanly parsed
	// frame — positions where a header is authoritative. While scanning
	// through garbage it is false, and header-shaped noise is skipped
	// rather than trusted.
	aligned bool
}

func newADTSReader(r io.Reader) *adtsReader {
	return &adtsReader{src: bufio.NewReaderSize(r, 16*1024), aligned: true}
}

const adtsHeaderLen = 7

// next returns the next AAC frame, scanning ahead to the next valid header
// when the stream is not aligned.
func (r *adtsReader) next() (adtsFrame, error) {
	for {
		hdr, err := r.src.Peek(adtsHeaderLen)
		if err != nil {
			return adtsFrame{}, err // io.EOF at a clean stream end
		}
		f, hdrLen, frameLen, err := parseADTSHeader(hdr)
		// The multiple-blocks error is only authoritative at an aligned
		// position; in mid-garbage scanning it is just header-shaped noise
		// whose block bits happen to be set.
		if errors.Is(err, errADTSNotAHeader) ||
			(errors.Is(err, errADTSMultipleBlocks) && !r.aligned) {
			_, _ = r.src.Discard(1)
			r.aligned = false
			continue
		}
		if err != nil {
			return adtsFrame{}, err
		}
		if _, err := r.src.Discard(hdrLen); err != nil {
			return adtsFrame{}, err
		}
		f.payload = make([]byte, frameLen-hdrLen)
		if _, err := io.ReadFull(r.src, f.payload); err != nil {
			return adtsFrame{}, err
		}
		r.aligned = true
		return f, nil
	}
}

// errADTSNotAHeader marks bytes that are not a valid ADTS header; the
// reader responds by scanning forward, unlike for hard errors.
var errADTSNotAHeader = errors.New("not an ADTS header")

// parseADTSHeader validates and decodes a 7-byte ADTS header, returning the
// frame parameters, the header length (9 when a CRC follows), and the total
// frame length including the header.
func parseADTSHeader(hdr []byte) (f adtsFrame, hdrLen, frameLen int, err error) {
	// Syncword (12 bits) and layer (2 bits, must be 0).
	if hdr[0] != 0xFF || hdr[1]&0xF6 != 0xF0 {
		return adtsFrame{}, 0, 0, errADTSNotAHeader
	}
	freqIdx := int(hdr[2]>>2) & 0xF
	if freqIdx >= len(adtsSampleRates) {
		return adtsFrame{}, 0, 0, errADTSNotAHeader
	}
	channelCfg := int(hdr[2]&1)<<2 | int(hdr[3]>>6)
	// 1 and 2 are plain mono/stereo. 0 (configuration signalled in-band) and
	// multichannel layouts do not occur on radio streams; treating them as
	// non-headers also keeps random syncword-like bytes from parsing.
	if channelCfg != 1 && channelCfg != 2 {
		return adtsFrame{}, 0, 0, errADTSNotAHeader
	}
	hdrLen = adtsHeaderLen
	if hdr[1]&0x01 == 0 { // protection_absent == 0: a CRC follows the header
		hdrLen += 2
	}
	frameLen = int(hdr[3]&0x3)<<11 | int(hdr[4])<<3 | int(hdr[5]>>5)
	if frameLen <= hdrLen {
		return adtsFrame{}, 0, 0, errADTSNotAHeader
	}
	if hdr[6]&0x3 != 0 { // number_of_raw_data_blocks_in_frame != 1
		return adtsFrame{}, 0, 0, errADTSMultipleBlocks
	}
	f.sampleRate = adtsSampleRates[freqIdx]
	f.channels = channelCfg
	return f, hdrLen, frameLen, nil
}
