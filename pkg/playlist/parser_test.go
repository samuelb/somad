package playlist

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"somad/internal/security/securitytest"
)

func TestGetStreamURLFromPlaylist(t *testing.T) {
	securitytest.AllowTestHosts(t)
	tests := []struct {
		name       string
		content    string
		statusCode int
		wantURL    string
		wantErr    bool
	}{
		{
			name: "valid pls file",
			content: `[playlist]
NumberOfEntries=1
File1=http://ice1.somafm.com/groovesalad-128-mp3
Title1=Groove Salad: A nicely chilled plate of ambient/downtempo beats and grooves.
Length1=-1
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "http://ice1.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name: "multiple entries",
			content: `[playlist]
NumberOfEntries=3
File1=http://ice1.somafm.com/groovesalad-128-mp3
Title1=Groove Salad
File2=http://ice2.somafm.com/groovesalad-128-mp3
Title2=Groove Salad (backup)
File3=http://ice3.somafm.com/groovesalad-128-mp3
Title3=Groove Salad (backup 2)
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "http://ice1.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name:       "empty file",
			content:    "",
			statusCode: http.StatusOK,
			wantURL:    "",
			wantErr:    true,
		},
		{
			name: "no File1 entry",
			content: `[playlist]
NumberOfEntries=0
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "",
			wantErr:    true,
		},
		{
			// Real-world playlists are not always spec-exact.
			name: "lenient parsing: case, whitespace, and File2 without File1",
			content: "[playlist]\r\n" +
				"NumberOfEntries=2\r\n" +
				"  FILE2 = http://ice2.somafm.com/groovesalad-128-mp3  \r\n" +
				"Title2=Groove Salad (backup)\r\n",
			statusCode: http.StatusOK,
			wantURL:    "http://ice2.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name: "Filename key is not a stream entry",
			content: `[playlist]
Filename=not-a-stream
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "",
			wantErr:    true,
		},
		{
			name: "prefers https entry over an earlier http entry",
			content: `[playlist]
NumberOfEntries=2
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=https://ice2.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "https://ice2.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name: "prefers the first https entry when several are present",
			content: `[playlist]
NumberOfEntries=3
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=https://ice2.somafm.com/groovesalad-128-mp3
File3=https://ice3.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "https://ice2.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name: "https match is case-insensitive",
			content: `[playlist]
NumberOfEntries=2
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=HTTPS://ice2.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "HTTPS://ice2.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name: "falls back to the first entry of any scheme when no https entry exists",
			content: `[playlist]
NumberOfEntries=2
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=http://ice2.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURL:    "http://ice1.somafm.com/groovesalad-128-mp3",
			wantErr:    false,
		},
		{
			name:       "server error",
			content:    "",
			statusCode: http.StatusInternalServerError,
			wantURL:    "",
			wantErr:    true,
		},
		{
			name:       "not found",
			content:    "Not Found",
			statusCode: http.StatusNotFound,
			wantURL:    "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.content))
			}))
			defer server.Close()

			got, err := GetStreamURLFromPlaylist(server.URL, "soma/test")

			if (err != nil) != tt.wantErr {
				t.Errorf("GetStreamURLFromPlaylist() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && got != tt.wantURL {
				t.Errorf("GetStreamURLFromPlaylist() = %v, want %v", got, tt.wantURL)
			}
		})
	}
}

func TestGetStreamURLFromPlaylistInvalidURL(t *testing.T) {
	_, err := GetStreamURLFromPlaylist("http://invalid-url-that-does-not-exist.example.com/playlist.pls", "soma/test")
	if err == nil {
		t.Error("GetStreamURLFromPlaylist() should return error for invalid URL")
	}
}
