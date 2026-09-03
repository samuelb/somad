package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersionSkewed(t *testing.T) {
	tests := []struct {
		name          string
		clientVersion string
		serverVersion string
		want          bool
	}{
		{"identical versions", "1.2.3", "1.2.3", false},
		{"different versions", "1.2.3", "1.2.4", true},
		{"dev client, released server", "dev", "1.2.3", false},
		{"released client, dev server", "1.2.3", "dev", false},
		{"dev client and dev server", "dev", "dev", false},
		{"empty client version differs", "", "1.2.3", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VersionSkewed(tt.clientVersion, tt.serverVersion))
		})
	}
}
