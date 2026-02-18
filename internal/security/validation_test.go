package security

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid somafm URL",
			url:     "https://somafm.com/channels.json",
			wantErr: false,
		},
		{
			name:    "valid somafm subdomain",
			url:     "https://ice1.somafm.com/stream",
			wantErr: false,
		},
		{
			name:    "valid http somafm URL",
			url:     "http://somafm.com/stream",
			wantErr: false,
		},
		{
			name:    "invalid external domain",
			url:     "https://evil.com/stream",
			wantErr: true,
		},
		{
			name:    "invalid scheme",
			url:     "ftp://somafm.com/file",
			wantErr: true,
		},
		{
			name:    "malformed URL",
			url:     "://invalid",
			wantErr: true,
		},
		{
			name:    "subdomain that looks like somafm",
			url:     "https://somafm.com.evil.com/stream",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePathNoTraversal(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid path",
			path:    "/home/user/.cache/somatui/file.json",
			wantErr: false,
		},
		{
			name:    "path with traversal",
			path:    "/home/user/.cache/../etc/passwd",
			wantErr: true,
		},
		{
			name:    "path with traversal in middle",
			path:    "/home/user/../other/file.json",
			wantErr: true,
		},
		{
			name:    "relative traversal",
			path:    "../../etc/passwd",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePathNoTraversal(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
