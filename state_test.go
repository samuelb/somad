package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setStateDir sets XDG_STATE_HOME to a temp dir for testing and returns a cleanup function.
func setStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

func TestLoadState_NoFile(t *testing.T) {
	setStateDir(t)

	state, err := LoadState()
	require.NoError(t, err)
	assert.NotNil(t, state)
	assert.Empty(t, state.LastSelectedChannelID)
}

func TestSaveAndLoadState_Roundtrip(t *testing.T) {
	setStateDir(t)

	original := &State{LastSelectedChannelID: "groovesalad"}
	err := SaveState(original)
	require.NoError(t, err)

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, original.LastSelectedChannelID, loaded.LastSelectedChannelID)
}

func TestSaveState_OverwritesExisting(t *testing.T) {
	setStateDir(t)

	err := SaveState(&State{LastSelectedChannelID: "dronezone"})
	require.NoError(t, err)

	err = SaveState(&State{LastSelectedChannelID: "secretagent"})
	require.NoError(t, err)

	loaded, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, "secretagent", loaded.LastSelectedChannelID)
}

func TestLoadState_CorruptJSON(t *testing.T) {
	dir := setStateDir(t)

	// Write corrupt data to the state file
	stateDir := filepath.Join(dir, appDirName)
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, stateFileName), []byte("{invalid json"), 0644))

	state, err := LoadState()
	assert.Error(t, err)
	assert.Nil(t, state)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestLoadState_EmptyJSON(t *testing.T) {
	dir := setStateDir(t)

	stateDir := filepath.Join(dir, appDirName)
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, stateFileName), []byte("{}"), 0644))

	state, err := LoadState()
	require.NoError(t, err)
	assert.NotNil(t, state)
	assert.Empty(t, state.LastSelectedChannelID)
}

func TestSaveState_CreatesDirectory(t *testing.T) {
	dir := setStateDir(t)

	err := SaveState(&State{LastSelectedChannelID: "test"})
	require.NoError(t, err)

	// Verify directory and file were created
	stateFile := filepath.Join(dir, appDirName, stateFileName)
	assert.FileExists(t, stateFile)

	// Verify content is valid JSON
	data, err := os.ReadFile(stateFile)
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
	setStateDir(t)

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
	dir := setStateDir(t)

	// Write state JSON without favorites field (simulates old version)
	stateDir := filepath.Join(dir, appDirName)
	require.NoError(t, os.MkdirAll(stateDir, 0755))
	oldJSON := `{"last_selected_channel_id": "groovesalad"}`
	require.NoError(t, os.WriteFile(filepath.Join(stateDir, stateFileName), []byte(oldJSON), 0644))

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
