package main

import (
	"testing"
)

func TestParseICYMetadata(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "standard format",
			input:   "StreamTitle='Artist - Song Title';StreamUrl='';",
			want:    "Artist - Song Title",
			wantErr: false,
		},
		{
			name:    "title only",
			input:   "StreamTitle='Just a Title';",
			want:    "Just a Title",
			wantErr: false,
		},
		{
			name:    "empty title",
			input:   "StreamTitle='';",
			want:    "",
			wantErr: false,
		},
		{
			name:    "with extra spaces",
			input:   "StreamTitle='  Spaced Artist - Spaced Title  ';",
			want:    "Spaced Artist - Spaced Title",
			wantErr: false,
		},
		{
			name:    "no StreamTitle",
			input:   "StreamUrl='http://example.com';",
			want:    "",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "multiple fields",
			input:   "StreamTitle='The Track';StreamUrl='http://foo';StreamGenre='Jazz';",
			want:    "The Track",
			wantErr: false,
		},
		{
			name:    "title with special characters",
			input:   "StreamTitle='Artist (feat. Other) - Song [Remix]';",
			want:    "Artist (feat. Other) - Song [Remix]",
			wantErr: false,
		},
		{
			name:    "unicode characters",
			input:   "StreamTitle='Café del Mar - Música Ambiental';",
			want:    "Café del Mar - Música Ambiental",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mr.parseICYMetadata(tt.input)

			if (err != nil) != tt.wantErr {
				t.Errorf("parseICYMetadata() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && got.Title != tt.want {
				t.Errorf("parseICYMetadata() title = %v, want %v", got.Title, tt.want)
			}
		})
	}
}

func TestNewMetadataReader(t *testing.T) {
	url := "http://example.com/stream"
	mr := NewMetadataReader(url)

	if mr.url != url {
		t.Errorf("NewMetadataReader url = %v, want %v", mr.url, url)
	}

	if mr.client == nil {
		t.Error("NewMetadataReader client should not be nil")
	}

	if mr.stopChan == nil {
		t.Error("NewMetadataReader stopChan should not be nil")
	}

	if mr.updateChan == nil {
		t.Error("NewMetadataReader updateChan should not be nil")
	}
}

func TestMetadataReaderGetUpdateChan(t *testing.T) {
	mr := NewMetadataReader("http://example.com/stream")
	ch := mr.GetUpdateChan()

	if ch == nil {
		t.Error("GetUpdateChan() should not return nil")
	}
}
