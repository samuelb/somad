package playlist

import (
	"strings"
	"testing"

	"somad/internal/security"
)

// FuzzParseStreamURLs guards the .pls parser, which reads bytes from a
// server that a redirect could have chosen: it must never panic, and every
// URL it returns must be a trimmed, non-empty value taken from a File entry,
// listed once, with the https entries first.
func FuzzParseStreamURLs(f *testing.F) {
	f.Add("[playlist]\nFile1=https://ice.somafm.com/groovesalad\nTitle1=x\n")
	f.Add("file1 = http://a\nFILE2=https://b\n")
	f.Add("File=nokey\nFile1=\nFile2=  \n")
	f.Add("")
	f.Add(strings.Repeat("File1=x\n", 1000))
	f.Fuzz(func(t *testing.T, content string) {
		urls, err := parseStreamURLs(strings.NewReader(content))
		if err != nil {
			return
		}
		seen := make(map[string]bool)
		pastHTTPS := false
		for _, url := range urls {
			if url == "" || url != strings.TrimSpace(url) {
				t.Fatalf("empty or untrimmed url %q", url)
			}
			if !strings.Contains(content, url) {
				t.Fatalf("url %q does not occur in the playlist", url)
			}
			if seen[url] {
				t.Fatalf("url %q listed twice", url)
			}
			seen[url] = true
			if !security.IsHTTPSURL(url) {
				pastHTTPS = true
			} else if pastHTTPS {
				t.Fatalf("https url %q listed after a non-https one in %q", url, urls)
			}
		}
	})
}
