package security

import (
	"strings"
	"unicode"
)

// SanitizeText makes text received from the network safe to print to a
// terminal. Stream titles, SomaFM's song history and the channel catalog
// reach `soma status`, `soma history` and the TUI as they came, so an
// escape sequence in one of them (from a tampered plain-http stream, say)
// would run in the user's terminal: write the clipboard, retitle the
// window, clear the screen. Line breaks and tabs become spaces; every other
// control character (C0, DEL and C1, which open those sequences) and
// invalid UTF-8 is dropped.
func SanitizeText(s string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsControl(r) {
			return r
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return -1
	}, strings.ToValidUTF8(s, ""))
}
