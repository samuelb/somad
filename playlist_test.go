package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetStreamURLFromPlaylist(t *testing.T) {
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

			got, err := getStreamURLFromPlaylist(server.URL)

			if (err != nil) != tt.wantErr {
				t.Errorf("getStreamURLFromPlaylist() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && got != tt.wantURL {
				t.Errorf("getStreamURLFromPlaylist() = %v, want %v", got, tt.wantURL)
			}
		})
	}
}

func TestGetStreamURLFromPlaylistInvalidURL(t *testing.T) {
	_, err := getStreamURLFromPlaylist("http://invalid-url-that-does-not-exist.example.com/playlist.pls")
	if err == nil {
		t.Error("getStreamURLFromPlaylist() should return error for invalid URL")
	}
}
