package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadLastfmSession_NoFile(t *testing.T) {
	SetStateDir(t)

	key, err := LoadLastfmSession()
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestSaveAndLoadLastfmSession_Roundtrip(t *testing.T) {
	SetStateDir(t)

	require.NoError(t, SaveLastfmSession("sess-abc123"))

	key, err := LoadLastfmSession()
	require.NoError(t, err)
	assert.Equal(t, "sess-abc123", key)
}

func TestSaveLastfmSession_Permissions(t *testing.T) {
	dir := SetStateDir(t)
	require.NoError(t, SaveLastfmSession("sess-abc123"))

	path := filepath.Join(dir, appDirName, lastfmFileName)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestSaveLastfmSession_OverwritesExisting(t *testing.T) {
	SetStateDir(t)

	require.NoError(t, SaveLastfmSession("first"))
	require.NoError(t, SaveLastfmSession("second"))

	key, err := LoadLastfmSession()
	require.NoError(t, err)
	assert.Equal(t, "second", key)
}

func TestClearLastfmSession_RemovesFile(t *testing.T) {
	dir := SetStateDir(t)
	require.NoError(t, SaveLastfmSession("sess-abc123"))

	require.NoError(t, ClearLastfmSession())

	path := filepath.Join(dir, appDirName, lastfmFileName)
	assert.NoFileExists(t, path)

	key, err := LoadLastfmSession()
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestClearLastfmSession_MissingFileIsNotAnError(t *testing.T) {
	SetStateDir(t)
	require.NoError(t, ClearLastfmSession())
}

func TestLoadLastfmSession_CorruptJSON(t *testing.T) {
	dir := SetStateDir(t)

	stateDir := filepath.Join(dir, appDirName)
	sessPath := filepath.Join(stateDir, lastfmFileName)
	require.NoError(t, os.MkdirAll(stateDir, 0755))                      // #nosec G301 // Test directory
	require.NoError(t, os.WriteFile(sessPath, []byte("{invalid"), 0644)) // #nosec G306 // Test file

	// A corrupt session file must not brick startup: it is moved aside for
	// inspection (like state.json, ADR-0012) and treated as "not logged in".
	key, err := LoadLastfmSession()
	require.NoError(t, err)
	assert.Empty(t, key)

	assert.NoFileExists(t, sessPath)
	backup, err := os.ReadFile(sessPath + ".corrupt") // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "{invalid", string(backup))

	// The next save must succeed and leave a loadable file behind.
	require.NoError(t, SaveLastfmSession("fresh"))
	key, err = LoadLastfmSession()
	require.NoError(t, err)
	assert.Equal(t, "fresh", key)
}
