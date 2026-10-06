package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTUIPrefs_DefaultsWithoutAFile(t *testing.T) {
	SetStateDir(t)
	prefs, err := LoadTUIPrefs()
	require.NoError(t, err)
	assert.Equal(t, TUIPrefs{}, prefs)
}

func TestTUIPrefs_Roundtrip(t *testing.T) {
	SetStateDir(t)
	require.NoError(t, SaveTUIPrefs(TUIPrefs{Visualizer: "mirror wave"}))
	prefs, err := LoadTUIPrefs()
	require.NoError(t, err)
	assert.Equal(t, "mirror wave", prefs.Visualizer)
}

func TestTUIPrefs_CorruptFileIsQuarantined(t *testing.T) {
	dir := SetStateDir(t)
	path := filepath.Join(dir, appDirName, tuiFileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	prefs, err := LoadTUIPrefs()
	require.NoError(t, err)
	assert.Equal(t, TUIPrefs{}, prefs)
	_, err = os.Stat(path + ".corrupt")
	assert.NoError(t, err, "moved aside")
}
