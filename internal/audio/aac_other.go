//go:build !darwin

package audio

import (
	"errors"
	"io"
)

// aacSupported reports whether this build can decode AAC streams. Only
// macOS ships a system AAC decoder (AudioToolbox); a Linux decoder would
// mean a new cgo library dependency, so those builds stream MP3.
const aacSupported = false

func newAACDecoder(io.Reader) (pcmDecoder, error) {
	return nil, errors.New("AAC decoding is not supported on this platform")
}
