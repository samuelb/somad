package protocol

import (
	"bufio"
	"encoding/json"
	"io"
)

// MaxLineBytes bounds a single server-to-client line. The channel catalog
// travels as one JSON line, so this must comfortably exceed channels.json
// (~100 KB).
const MaxLineBytes = 4 << 20

// MaxRequestBytes bounds a single client-to-server line. Requests are tiny
// (a method name and a channel ID at most), so the server never needs the
// catalog-sized budget, and a small cap keeps what an unauthenticated peer
// can make it buffer small.
const MaxRequestBytes = 64 << 10

// WriteLine marshals v and writes it as one newline-terminated line in a
// single Write call, so concurrent writers on the same goroutine-safe writer
// never interleave partial lines.
func WriteLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// NewScanner returns a line scanner sized for server-to-client messages.
func NewScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), MaxLineBytes)
	return sc
}

// NewRequestScanner returns a line scanner sized for client-to-server
// requests; a longer line ends the scan with bufio.ErrTooLong.
func NewRequestScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4*1024), MaxRequestBytes)
	return sc
}
