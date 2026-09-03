package audio

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// The fuzz targets below guard the parsers that consume bytes straight off
// the network: none of them may panic or loop forever on hostile input.

func FuzzParseICYMetadata(f *testing.F) {
	f.Add("StreamTitle='Artist - Title';StreamUrl='';")
	f.Add("StreamTitle='';")
	f.Add("StreamTitle='Rock'; Roll';")
	f.Add("StreamUrl='http://x';")
	f.Add("")
	f.Fuzz(func(t *testing.T, meta string) {
		info, err := parseICYMetadata(meta)
		if err != nil && info.Title != "" {
			t.Fatalf("title %q returned alongside error %v", info.Title, err)
		}
	})
}

func FuzzICYDemuxer(f *testing.F) {
	b := &icyStreamBuilder{icyInt: 16}
	b.segment(0xAA, "StreamTitle='Seed';").segment(0xBB, "")
	f.Add(b.buf.Bytes(), 16)
	f.Add([]byte{0xFF, 0x01}, 1)
	f.Add([]byte{}, 8)
	f.Fuzz(func(t *testing.T, data []byte, icyInt int) {
		if icyInt <= 0 || icyInt > 1<<16 {
			return // newICYDemuxer is only ever built from a positive header value
		}
		var titles int
		d := newICYDemuxer(bytes.NewReader(data), icyInt, func(string) { titles++ })
		audio, err := io.ReadAll(d)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("unexpected error class: %v", err)
		}
		if len(audio) > len(data) {
			t.Fatalf("demuxer produced %d audio bytes from %d input bytes", len(audio), len(data))
		}
		if titles > len(data) {
			t.Fatalf("%d titles from %d bytes", titles, len(data))
		}
	})
}

func FuzzADTSReader(f *testing.F) {
	f.Add(append(buildADTSFrame(2, []byte("first")), buildADTSFrame(1, []byte("second"))...))
	f.Add([]byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC})
	f.Add(bytes.Repeat([]byte{0xFF}, 64))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		r := newADTSReader(bytes.NewReader(data))
		var consumed int
		for {
			fr, err := r.next()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, errADTSMultipleBlocks) {
					t.Fatalf("unexpected error class: %v", err)
				}
				return
			}
			if len(fr.payload) == 0 {
				t.Fatal("frame with empty payload")
			}
			if fr.channels != 1 && fr.channels != 2 {
				t.Fatalf("channels %d", fr.channels)
			}
			consumed += adtsHeaderLen + len(fr.payload)
			if consumed > len(data) {
				t.Fatalf("frames account for %d bytes, input has %d", consumed, len(data))
			}
		}
	})
}
