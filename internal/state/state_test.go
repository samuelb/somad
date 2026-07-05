package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SetStateDir sets XDG_STATE_HOME to a temp dir for testing and returns a cleanup function.
func SetStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

func TestLoadState_NoFile(t *testing.T) {
	SetStateDir(t)

	state, err := LoadState()
	require.NoError(t, err)
	assert.NotNil(t, state)
	assert.Empty(t, state.LastSelectedChannelID)
}

func TestSaveAndLoadState_Roundtrip(t *testing.T) {
	SetStateDir(t)

	original := &State{LastSelectedChannelID: "groovesalad"}
	err := SaveState(original)
	require.NoError(t, err)

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, original.LastSelectedChannelID, loaded.LastSelectedChannelID)
}

func TestSaveState_OverwritesExisting(t *testing.T) {
	SetStateDir(t)

	err := SaveState(&State{LastSelectedChannelID: "dronezone"})
	require.NoError(t, err)

	err = SaveState(&State{LastSelectedChannelID: "secretagent"})
	require.NoError(t, err)

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, "secretagent", loaded.LastSelectedChannelID)
}

func TestGetVolume_DefaultsToFull(t *testing.T) {
	s := &State{}
	assert.Equal(t, 1.0, s.GetVolume())
}

func TestGetVolume_ClampsStoredValues(t *testing.T) {
	s := &State{}

	s.SetVolume(-0.5)
	assert.Zero(t, s.GetVolume())

	s.SetVolume(2.0)
	assert.Equal(t, 1.0, s.GetVolume())
}

func TestSetVolume_ZeroIsDistinctFromUnset(t *testing.T) {
	s := &State{}
	s.SetVolume(0)

	assert.Zero(t, s.GetVolume(), "an explicit mute must not fall back to full volume")
}

func TestSaveAndLoadState_WithVolume(t *testing.T) {
	SetStateDir(t)

	original := &State{}
	original.SetVolume(0.65)
	require.NoError(t, SaveState(original))

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.InDelta(t, 0.65, loaded.GetVolume(), 1e-9)
}

func TestLoadState_NoVolumeField(t *testing.T) {
	SetStateDir(t)

	// A pre-volume state file must default to full volume.
	require.NoError(t, SaveState(&State{LastSelectedChannelID: "groovesalad"}))

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, 1.0, loaded.GetVolume())
}

func TestLoadState_CorruptJSON(t *testing.T) {
	dir := SetStateDir(t)

	// Write corrupt data to the state file
	stateDir := filepath.Join(dir, appDirName)
	statePath := filepath.Join(stateDir, stateFileName)
	require.NoError(t, os.MkdirAll(stateDir, 0755))                            // #nosec G301 // Test directory
	require.NoError(t, os.WriteFile(statePath, []byte("{invalid json"), 0644)) // #nosec G306 // Test file

	// A corrupt state file must not brick startup: it is moved aside for
	// inspection and a fresh state is returned.
	state, err := LoadState()
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Empty(t, state.LastSelectedChannelID)

	assert.NoFileExists(t, statePath)
	backup, err := os.ReadFile(statePath + ".corrupt") // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "{invalid json", string(backup))

	// The next save must succeed and leave a loadable file behind.
	require.NoError(t, SaveState(&State{LastSelectedChannelID: "groovesalad"}))
	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, "groovesalad", loaded.LastSelectedChannelID)
}

func TestLoadState_EmptyJSON(t *testing.T) {
	dir := SetStateDir(t)

	stateDir := filepath.Join(dir, appDirName)
	require.NoError(t, os.MkdirAll(stateDir, 0755))                                              // #nosec G301 // Test directory
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, stateFileName), []byte("{}"), 0644)) // #nosec G306 // Test file

	state, err := LoadState()
	require.NoError(t, err)
	assert.NotNil(t, state)
	assert.Empty(t, state.LastSelectedChannelID)
}

func TestSaveState_CreatesDirectory(t *testing.T) {
	dir := SetStateDir(t)

	err := SaveState(&State{LastSelectedChannelID: "test"})
	require.NoError(t, err)

	// Verify directory and file were created
	stateFile := filepath.Join(dir, appDirName, stateFileName)
	assert.FileExists(t, stateFile)

	// Verify content is valid JSON
	data, err := os.ReadFile(stateFile) // #nosec G304 // Test file path
	require.NoError(t, err)
	var state State
	require.NoError(t, json.Unmarshal(data, &state))
	assert.Equal(t, "test", state.LastSelectedChannelID)
}

func TestIsFavorite(t *testing.T) {
	state := &State{FavoriteChannelIDs: []string{"groovesalad", "dronezone"}}

	assert.True(t, state.IsFavorite("groovesalad"))
	assert.True(t, state.IsFavorite("dronezone"))
	assert.False(t, state.IsFavorite("secretagent"))
	assert.False(t, state.IsFavorite(""))
}

func TestIsFavorite_Empty(t *testing.T) {
	state := &State{}
	assert.False(t, state.IsFavorite("groovesalad"))
}

func TestToggleFavorite_Add(t *testing.T) {
	state := &State{}

	state.ToggleFavorite("groovesalad")
	assert.True(t, state.IsFavorite("groovesalad"))
	assert.Equal(t, []string{"groovesalad"}, state.FavoriteChannelIDs)
}

func TestToggleFavorite_Remove(t *testing.T) {
	state := &State{FavoriteChannelIDs: []string{"groovesalad", "dronezone"}}

	state.ToggleFavorite("groovesalad")
	assert.False(t, state.IsFavorite("groovesalad"))
	assert.True(t, state.IsFavorite("dronezone"))
	assert.Equal(t, []string{"dronezone"}, state.FavoriteChannelIDs)
}

func TestToggleFavorite_AddAndRemove(t *testing.T) {
	state := &State{}

	state.ToggleFavorite("groovesalad")
	assert.True(t, state.IsFavorite("groovesalad"))

	state.ToggleFavorite("groovesalad")
	assert.False(t, state.IsFavorite("groovesalad"))
	assert.Empty(t, state.FavoriteChannelIDs)
}

func TestSaveAndLoadState_WithFavorites(t *testing.T) {
	SetStateDir(t)

	original := &State{
		LastSelectedChannelID: "groovesalad",
		FavoriteChannelIDs:    []string{"dronezone", "secretagent"},
	}
	err := SaveState(original)
	require.NoError(t, err)

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, original.LastSelectedChannelID, loaded.LastSelectedChannelID)
	assert.Equal(t, original.FavoriteChannelIDs, loaded.FavoriteChannelIDs)
}

func TestLoadState_BackwardCompatibility(t *testing.T) {
	dir := SetStateDir(t)

	// Write state JSON without favorites field (simulates old version)
	stateDir := filepath.Join(dir, appDirName)
	require.NoError(t, os.MkdirAll(stateDir, 0755)) // #nosec G301 // Test directory
	oldJSON := `{"last_selected_channel_id": "groovesalad"}`
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, stateFileName), []byte(oldJSON), 0644)) // #nosec G306 // Test file

	state, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, "groovesalad", state.LastSelectedChannelID)
	assert.Empty(t, state.FavoriteChannelIDs)
	assert.False(t, state.IsFavorite("groovesalad"))
}

func TestGetStateDir_XDGOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/custom-state")

	dir, err := getStateDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/tmp/custom-state", appDirName), dir)
}

func TestGetLogFilePath(t *testing.T) {
	dir := SetStateDir(t)

	path, err := GetLogFilePath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, appDirName, "server.log"), path)

	// Creates the state directory as a side effect, like GetStateFilePath.
	assert.DirExists(t, filepath.Dir(path))
}
