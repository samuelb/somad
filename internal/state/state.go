package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"somad/internal/atomicfile"
	"somad/internal/xdg"
)

// State holds application state that persists between sessions.
type State struct {
	LastSelectedChannelID string   `json:"last_selected_channel_id"`
	FavoriteChannelIDs    []string `json:"favorite_channel_ids,omitempty"`
	// Volume is a pointer so an explicit 0 (muted) is distinguishable from
	// "never set" (which defaults to full volume).
	Volume *float64 `json:"volume,omitempty"`
	// PreMuteVolume is the volume to restore on the next mute toggle. It is
	// set when ToggleMute mutes (drops volume to 0) and cleared by any
	// explicit SetVolume to a non-zero level, so a level chosen after a mute
	// is never silently overridden by an old pre-mute value.
	PreMuteVolume *float64 `json:"pre_mute_volume,omitempty"`
}

// Clone returns an independent copy suitable for saving without holding the
// caller's lock.
func (s *State) Clone() *State {
	if s == nil {
		return &State{}
	}
	clone := &State{
		LastSelectedChannelID: s.LastSelectedChannelID,
		FavoriteChannelIDs:    slices.Clone(s.FavoriteChannelIDs),
	}
	if s.Volume != nil {
		v := *s.Volume
		clone.Volume = &v
	}
	if s.PreMuteVolume != nil {
		v := *s.PreMuteVolume
		clone.PreMuteVolume = &v
	}
	return clone
}

// GetVolume returns the persisted volume clamped to [0, 1], defaulting to
// full volume when unset.
func (s *State) GetVolume() float64 {
	if s.Volume == nil {
		return 1
	}
	v := *s.Volume
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// SetVolume stores the volume for the next session. A non-zero level clears
// any stored pre-mute volume: it is a deliberate new level, not an unmute,
// so a stale pre-mute value must not resurface on a later mute toggle.
func (s *State) SetVolume(v float64) {
	s.Volume = &v
	if v > 0 {
		s.PreMuteVolume = nil
	}
}

// MuteVolume remembers v as the level to restore on the next unmute.
func (s *State) MuteVolume(v float64) {
	s.PreMuteVolume = &v
}

// UnmuteVolume returns the level to restore when unmuting: the remembered
// pre-mute volume, or a sensible default when nothing was remembered (e.g.
// volume reached 0 through explicit steps rather than a mute toggle).
func (s *State) UnmuteVolume() float64 {
	if s.PreMuteVolume == nil {
		return 1.0
	}
	return *s.PreMuteVolume
}

// ToggleFavorite adds or removes a channel ID from the favorites list. It is
// copy-on-write: the old backing array is left untouched, so slice headers
// handed out before the call (snapshots, clones in flight) never mutate
// under their holders.
func (s *State) ToggleFavorite(id string) {
	if i := slices.Index(s.FavoriteChannelIDs, id); i >= 0 {
		s.FavoriteChannelIDs = slices.Delete(slices.Clone(s.FavoriteChannelIDs), i, i+1)
		return
	}
	s.FavoriteChannelIDs = append(slices.Clone(s.FavoriteChannelIDs), id)
}

// FavoriteSet turns a favorites list (from State.FavoriteChannelIDs, or a
// protocol.ChannelsPayload's own copy of it) into a set for O(1) membership
// checks when marking up a channel list.
func FavoriteSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

const (
	stateFileName = "state.json"
	appDirName    = "somad"
)

// getStateDir returns the directory for storing application state.
// On Linux: $XDG_STATE_HOME/somad or ~/.local/state/somad
// On macOS: ~/Library/Application Support/somad
func getStateDir() (string, error) {
	return xdg.StateDir(appDirName)
}

// Dir returns the application state directory, creating it if needed. The
// server also keeps its auto-generated TLS certificate here.
func Dir() (string, error) {
	stateDir, err := getStateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create state directory: %w", err)
	}
	return stateDir, nil
}

// GetStateFilePath returns the absolute path to the state file.
func GetStateFilePath() (string, error) {
	return statePath(stateFileName)
}

// GetLogFilePath returns the server log file path, kept in the state
// directory next to state.json.
func GetLogFilePath() (string, error) {
	return statePath("server.log")
}

// statePath returns the path of name inside the state directory, creating
// the directory if needed.
func statePath(name string) (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// LoadState reads the application state from the state file. A missing
// file yields a default empty State; a corrupt one is moved aside
// (ADR-0012) and likewise yields an empty State, so it never bricks startup.
func LoadState() (*State, error) {
	statePath, err := GetStateFilePath()
	if err != nil {
		return nil, err
	}
	var state State
	switch err := atomicfile.ReadJSON(statePath, &state); {
	case err == nil:
		return &state, nil
	case errors.Is(err, fs.ErrNotExist):
		return &State{}, nil
	case errors.Is(err, atomicfile.ErrCorrupt):
		atomicfile.Quarantine(statePath, "state file", err)
		return &State{}, nil
	default:
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}
}

// SaveState writes the given application state to the state file.
func SaveState(state *State) error {
	statePath, err := GetStateFilePath()
	if err != nil {
		return err
	}
	// Atomic write: a crash mid-save must not corrupt the state file.
	if err := atomicfile.WriteJSON(statePath, state, 0600); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}
	return nil
}
