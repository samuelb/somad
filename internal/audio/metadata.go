package audio

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// TrackInfo represents the current track information from ICY metadata.
type TrackInfo struct {
	Title string
	// Gen is the generation of the session the title arrived on (see
	// Player), so a title from a stream that is still fading out is not
	// shown under its successor.
	Gen uint64
}

// icyDemuxer strips the ICY metadata blocks that Shoutcast/Icecast servers
// interleave into the audio stream (when requested via Icy-MetaData: 1),
// forwarding pure audio bytes to the decoder and reporting title changes.
// This lets one connection serve both playback and now-playing info.
type icyDemuxer struct {
	src       *bufio.Reader
	icyInt    int // audio bytes between metadata blocks
	remaining int // audio bytes left until the next metadata block
	onTitle   func(string)
	lastTitle string
	gotTitle  bool
}

// newICYDemuxer wraps src, whose audio is interrupted by a metadata block
// every icyInt bytes. onTitle is invoked whenever the stream title changes.
func newICYDemuxer(src io.Reader, icyInt int, onTitle func(string)) *icyDemuxer {
	return &icyDemuxer{
		src:       bufio.NewReader(src),
		icyInt:    icyInt,
		remaining: icyInt,
		onTitle:   onTitle,
	}
}

func (d *icyDemuxer) Read(p []byte) (int, error) {
	if d.remaining == 0 {
		if err := d.readMetadataBlock(); err != nil {
			return 0, err
		}
		d.remaining = d.icyInt
	}
	if len(p) > d.remaining {
		p = p[:d.remaining]
	}
	n, err := d.src.Read(p)
	d.remaining -= n
	return n, err
}

// readMetadataBlock consumes one metadata block from the stream. A zero
// length byte means "no change". Malformed metadata is skipped, not fatal —
// the audio around it is still good.
func (d *icyDemuxer) readMetadataBlock() error {
	lenByte, err := d.src.ReadByte()
	if err != nil {
		return err
	}
	metaLen := int(lenByte) * 16
	if metaLen == 0 {
		return nil
	}

	block := make([]byte, metaLen)
	if _, err := io.ReadFull(d.src, block); err != nil {
		return err
	}

	info, err := parseICYMetadata(strings.TrimRight(string(block), "\x00"))
	if err != nil {
		return nil
	}
	if !d.gotTitle || info.Title != d.lastTitle {
		d.gotTitle = true
		d.lastTitle = info.Title
		if d.onTitle != nil {
			d.onTitle(info.Title)
		}
	}
	return nil
}

// parseICYMetadata parses an ICY metadata string and extracts the title.
func parseICYMetadata(metaStr string) (TrackInfo, error) {
	// ICY metadata format: StreamTitle='Title';StreamUrl='';
	// The title itself may contain semicolons, so it is delimited by the
	// closing "';" sequence rather than a bare ";" — splitting on ";" would
	// truncate titles like "Artist - A; B". This is a known ambiguity of the
	// format itself: ICY provides no escaping, so a title containing a
	// literal "';" (say "Rock'; Roll") is truncated at that point. Nothing
	// a parser can do recovers the intent.
	const opener = "StreamTitle='"
	start := strings.Index(metaStr, opener)
	if start < 0 {
		return TrackInfo{}, fmt.Errorf("no StreamTitle found in metadata")
	}
	start += len(opener)

	title := metaStr[start:]
	if end := strings.Index(title, "';"); end >= 0 {
		// Closing "';" found: the title is everything up to it.
		title = title[:end]
	} else {
		// Title is the final field: drop a trailing "'" if present.
		title = strings.TrimSuffix(title, "'")
	}

	return TrackInfo{
		Title: strings.TrimSpace(title),
	}, nil
}
