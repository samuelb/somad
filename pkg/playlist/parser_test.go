package playlist

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"somad/internal/security/securitytest"
)

func TestGetStreamURLsFromPlaylist(t *testing.T) {
	securitytest.AllowTestHosts(t)
	tests := []struct {
		name       string
		content    string
		statusCode int
		wantURLs   []string
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
			wantURLs:   []string{"http://ice1.somafm.com/groovesalad-128-mp3"},
			wantErr:    false,
		},
		{
			name: "multiple entries: every mirror, in playlist order",
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
			wantURLs: []string{
				"http://ice1.somafm.com/groovesalad-128-mp3",
				"http://ice2.somafm.com/groovesalad-128-mp3",
				"http://ice3.somafm.com/groovesalad-128-mp3",
			},
			wantErr: false,
		},
		{
			name:       "empty file",
			content:    "",
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name: "no File1 entry",
			content: `[playlist]
NumberOfEntries=0
Version=2`,
			statusCode: http.StatusOK,
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
			wantURLs:   []string{"http://ice2.somafm.com/groovesalad-128-mp3"},
			wantErr:    false,
		},
		{
			name: "Filename key is not a stream entry",
			content: `[playlist]
Filename=not-a-stream
Version=2`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name: "https entries come before earlier http entries",
			content: `[playlist]
NumberOfEntries=2
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=https://ice2.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURLs: []string{
				"https://ice2.somafm.com/groovesalad-128-mp3",
				"http://ice1.somafm.com/groovesalad-128-mp3",
			},
			wantErr: false,
		},
		{
			name: "several https entries keep their order",
			content: `[playlist]
NumberOfEntries=3
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=https://ice2.somafm.com/groovesalad-128-mp3
File3=https://ice3.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURLs: []string{
				"https://ice2.somafm.com/groovesalad-128-mp3",
				"https://ice3.somafm.com/groovesalad-128-mp3",
				"http://ice1.somafm.com/groovesalad-128-mp3",
			},
			wantErr: false,
		},
		{
			name: "https match is case-insensitive",
			content: `[playlist]
NumberOfEntries=2
File1=http://ice1.somafm.com/groovesalad-128-mp3
File2=HTTPS://ice2.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURLs: []string{
				"HTTPS://ice2.somafm.com/groovesalad-128-mp3",
				"http://ice1.somafm.com/groovesalad-128-mp3",
			},
			wantErr: false,
		},
		{
			name: "duplicate entries are listed once",
			content: `[playlist]
NumberOfEntries=3
File1=https://ice1.somafm.com/groovesalad-128-mp3
File2=https://ice2.somafm.com/groovesalad-128-mp3
File3=https://ice1.somafm.com/groovesalad-128-mp3
Version=2`,
			statusCode: http.StatusOK,
			wantURLs: []string{
				"https://ice1.somafm.com/groovesalad-128-mp3",
				"https://ice2.somafm.com/groovesalad-128-mp3",
			},
			wantErr: false,
		},
		{
			name:       "server error",
			content:    "",
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
		{
			name:       "not found",
			content:    "Not Found",
			statusCode: http.StatusNotFound,
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

			got, err := GetStreamURLsFromPlaylist(server.URL, "soma/test")

			if (err != nil) != tt.wantErr {
				t.Errorf("GetStreamURLsFromPlaylist() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && !slices.Equal(got, tt.wantURLs) {
				t.Errorf("GetStreamURLsFromPlaylist() = %v, want %v", got, tt.wantURLs)
			}
		})
	}
}

func TestGetStreamURLsFromPlaylistInvalidURL(t *testing.T) {
	_, err := GetStreamURLsFromPlaylist("http://invalid-url-that-does-not-exist.example.com/playlist.pls", "soma/test")
	if err == nil {
		t.Error("GetStreamURLsFromPlaylist() should return error for invalid URL")
	}
}
