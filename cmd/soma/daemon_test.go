package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePSK_ReturnsDistinctHexKeys(t *testing.T) {
	a, err := generatePSK()
	require.NoError(t, err)
	b, err := generatePSK()
	require.NoError(t, err)

	// pskBytes bytes, hex-encoded, is twice as many characters.
	assert.Len(t, a, pskBytes*2)
	assert.NotEqual(t, a, b, "two generated keys should not collide")
	// readPSKFile must accept it unchanged: no whitespace to trim beyond
	// the trailing newline writeGeneratedPSK adds.
	assert.NotContains(t, a, " ")
}

func TestWriteGeneratedPSK_WritesPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")

	require.NoError(t, writeGeneratedPSK(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// The written file must itself pass the permission check and round-trip
	// through readPSKFile as a single trimmed line.
	psk, err := readPSKFile(path)
	require.NoError(t, err)
	assert.Len(t, psk, pskBytes*2)
}

func TestWriteGeneratedPSK_RefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o600))

	err := writeGeneratedPSK(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	data, err := os.ReadFile(path) // #nosec G304 -- test-controlled path
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(data), "the existing file must be left untouched")
}
