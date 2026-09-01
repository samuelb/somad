package audio

import (
	"bytes"
	"io"
	"testing"
)

// buildADTSFrame assembles a syntactically valid ADTS frame around the given
// payload: 44.1 kHz, AAC-LC, no CRC.
func buildADTSFrame(channels int, payload []byte) []byte {
	frameLen := adtsHeaderLen + len(payload)
	b2 := 0x40 | 4<<2 | (channels>>2)&1 // profile LC (1), freq index 4 (44100)
	b3 := (channels&0x3)<<6 | (frameLen>>11)&0x3
	b4 := (frameLen >> 3) & 0xFF
	b5 := (frameLen&0x7)<<5 | 0x1F
	frame := make([]byte, 0, frameLen)
	// 0xF1: MPEG-4, layer 0, protection_absent=1.
	// 0xFC: buffer fullness low bits, 1 raw data block.
	frame = append(frame, 0xFF, 0xF1, byte(b2&0xFF), byte(b3&0xFF), byte(b4&0xFF), byte(b5&0xFF), 0xFC)
	return append(frame, payload...)
}

func TestADTSReaderParsesFrames(t *testing.T) {
	stream := append(buildADTSFrame(2, []byte("first-payload")), buildADTSFrame(2, []byte("second"))...)
	r := newADTSReader(bytes.NewReader(stream))

	f, err := r.next()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if f.sampleRate != 44100 || f.channels != 2 || string(f.payload) != "first-payload" {
		t.Fatalf("first frame = %d Hz, %d ch, %q", f.sampleRate, f.channels, f.payload)
	}

	f, err = r.next()
	if err != nil {
		t.Fatalf("second frame: %v", err)
	}
	if string(f.payload) != "second" {
		t.Fatalf("second payload = %q", f.payload)
	}

	if _, err := r.next(); err != io.EOF {
		t.Fatalf("after last frame err = %v, want io.EOF", err)
	}
}

func TestADTSReaderResyncsPastGarbage(t *testing.T) {
	stream := append([]byte{0xFF, 0xF1, 0xDE, 0xAD, 0x01, 0x02}, buildADTSFrame(1, []byte("mono"))...)
	stream = append([]byte("leading junk"), stream...)
	r := newADTSReader(bytes.NewReader(stream))

	f, err := r.next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if f.channels != 1 || string(f.payload) != "mono" {
		t.Fatalf("frame = %d ch, %q", f.channels, f.payload)
	}
}

func TestADTSReaderSkipsCRC(t *testing.T) {
	payload := []byte("crc-framed")
	frameLen := adtsHeaderLen + 2 + len(payload)
	b3 := 2<<6 | (frameLen>>11)&0x3
	b4 := (frameLen >> 3) & 0xFF
	b5 := (frameLen&0x7)<<5 | 0x1F
	frame := make([]byte, 0, frameLen)
	frame = append(frame,
		0xFF,
		0xF0, // protection_absent=0: 2 CRC bytes follow the header
		0x40|4<<2,
		byte(b3&0xFF), byte(b4&0xFF), byte(b5&0xFF),
		0xFC,
		0xAB, 0xCD, // CRC
	)
	frame = append(frame, payload...)

	f, err := newADTSReader(bytes.NewReader(frame)).next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if string(f.payload) != "crc-framed" {
		t.Fatalf("payload = %q, want %q (CRC not stripped?)", f.payload, payload)
	}
}

func TestADTSReaderRejectsMultipleRawDataBlocks(t *testing.T) {
	frame := buildADTSFrame(2, []byte("multi"))
	frame[6] |= 0x1 // number_of_raw_data_blocks_in_frame = 2

	if _, err := newADTSReader(bytes.NewReader(frame)).next(); err != errADTSMultipleBlocks {
		t.Fatalf("err = %v, want errADTSMultipleBlocks", err)
	}
}

func TestADTSReaderTruncatedPayload(t *testing.T) {
	frame := buildADTSFrame(2, []byte("cut off here"))
	if _, err := newADTSReader(bytes.NewReader(frame[:len(frame)-4])).next(); err == nil {
		t.Fatal("truncated payload parsed without error")
	}
}

func TestADTSReaderPureGarbageEndsWithEOF(t *testing.T) {
	garbage := bytes.Repeat([]byte{0xFF, 0x00, 0x13, 0x37}, 1024)
	if _, err := newADTSReader(bytes.NewReader(garbage)).next(); err != io.EOF {
		t.Fatalf("err = %v, want io.EOF after scanning garbage", err)
	}
}
