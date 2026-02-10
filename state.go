package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

// State holds application state that persists between sessions.
type State struct {
	LastSelectedChannelID string   `json:"last_selected_channel_id"`
	FavoriteChannelIDs    []string `json:"favorite_channel_ids,omitempty"`
}

// IsFavorite returns true if the given channel ID is in the favorites list.
func (s *State) IsFavorite(id string) bool {
	return slices.Contains(s.FavoriteChannelIDs, id)
}

// ToggleFavorite adds or removes a channel ID from the favorites list.
func (s *State) ToggleFavorite(id string) {
	for i, fav := range s.FavoriteChannelIDs {
		if fav == id {
			s.FavoriteChannelIDs = append(s.FavoriteChannelIDs[:i], s.FavoriteChannelIDs[i+1:]...)
			return
		}
	}
	s.FavoriteChannelIDs = append(s.FavoriteChannelIDs, id)
}

const (
	stateFileName = "state.json"
	appDirName    = "somatui"
)

// getStateDir returns the directory for storing application state.
// On Linux: $XDG_STATE_HOME/somatui or ~/.local/state/somatui
// On macOS: ~/Library/Application Support/somatui
func getStateDir() (string, error) {
	var baseDir string

	if runtime.GOOS == "darwin" {
		// macOS: use Application Support
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, "Library", "Application Support")
	} else {
		// Linux/other: use XDG_STATE_HOME or fallback to ~/.local/state
		if xdgState := os.Getenv("XDG_STATE_HOME"); xdgState != "" {
			baseDir = xdgState
		} else {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get home directory: %w", err)
			}
			baseDir = filepath.Join(homeDir, ".local", "state")
		}
	}

	return filepath.Join(baseDir, appDirName), nil
}

// getStateFilePath returns the absolute path to the state file.
func getStateFilePath() (string, error) {
	stateDir, err := getStateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create state directory: %w", err)
	}
	return filepath.Join(stateDir, stateFileName), nil
}

// LoadState reads the application state from the state file.
// If the file does not exist, it returns a default empty State.
func LoadState() (*State, error) {
	statePath, err := getStateFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state data: %w", err)
	}

	return &state, nil
}

// SaveState writes the given application state to the state file.
func SaveState(state *State) error {
	statePath, err := getStateFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state for saving: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state to file: %w", err)
	}

	return nil
}
