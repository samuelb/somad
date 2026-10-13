package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text unchanged", in: "Artist - Title", want: "Artist - Title"},
		{name: "unicode unchanged", in: "Sigur Rós — Hoppípolla ♪", want: "Sigur Rós — Hoppípolla ♪"},
		{name: "no-break space kept", in: "a b", want: "a b"},
		{name: "line breaks and tabs become spaces", in: "a\nb\r\nc\td", want: "a b  c d"},
		{name: "CSI sequence loses its ESC", in: "\x1b[2JTitle", want: "[2JTitle"},
		{name: "OSC 52 clipboard write disarmed", in: "\x1b]52;c;aGk=\x07Song", want: "]52;c;aGk=Song"},
		{name: "C1 CSI dropped", in: "a\u009b31mb", want: "a31mb"},
		{name: "DEL and NUL dropped", in: "a\x7fb\x00c", want: "abc"},
		{name: "invalid UTF-8 dropped", in: "a\xffb\xc3", want: "ab"},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeText(tt.in))
		})
	}
}
