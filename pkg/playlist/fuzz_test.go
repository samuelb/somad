package playlist

import (
	"strings"
	"testing"
)

// FuzzParseFirstStreamURL guards the .pls parser, which reads bytes from a
// server that a redirect could have chosen: it must never panic, and any
// URL it returns must be a trimmed, non-empty value taken from a File entry.
func FuzzParseFirstStreamURL(f *testing.F) {
	f.Add("[playlist]\nFile1=https://ice.somafm.com/groovesalad\nTitle1=x\n")
	f.Add("file1 = http://a\nFILE2=https://b\n")
	f.Add("File=nokey\nFile1=\nFile2=  \n")
	f.Add("")
	f.Add(strings.Repeat("File1=x\n", 1000))
	f.Fuzz(func(t *testing.T, content string) {
		url, err := parseFirstStreamURL(strings.NewReader(content))
		if err != nil {
			return
		}
		if url != strings.TrimSpace(url) {
			t.Fatalf("untrimmed url %q", url)
		}
		if url != "" && !strings.Contains(content, url) {
			t.Fatalf("url %q does not occur in the playlist", url)
		}
	})
}
