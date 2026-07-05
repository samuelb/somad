package protocol

import (
	"bufio"
	"encoding/json"
	"io"
)

// MaxLineBytes bounds a single protocol line. The channel catalog travels as
// one JSON line, so this must comfortably exceed channels.json (~100 KB).
const MaxLineBytes = 4 << 20

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

// NewScanner returns a line scanner sized for protocol messages.
func NewScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), MaxLineBytes)
	return sc
}
