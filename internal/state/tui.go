package state

import (
	"errors"
	"fmt"
	"io/fs"

	"somad/internal/atomicfile"
)

// tuiFileName holds the terminal UI's own preferences. They belong to the
// client, not the daemon: a TUI on a laptop driving a remote daemon keeps
// its own, and nothing about playback lives here (ADR-0003).
const tuiFileName = "tui.json"

// TUIPrefs are the terminal UI's remembered preferences.
type TUIPrefs struct {
	// Visualizer names the spectrum visualizer style last picked with v
	// (ui.VisualizerMode's String); empty or unknown means off.
	Visualizer string `json:"visualizer,omitempty"`
}

// LoadTUIPrefs reads the TUI's preferences. A missing file is not an
// error and yields the defaults; a corrupt one is quarantined
// (ADR-0012) and treated as missing.
func LoadTUIPrefs() (TUIPrefs, error) {
	path, err := statePath(tuiFileName)
	if err != nil {
		return TUIPrefs{}, err
	}
	var prefs TUIPrefs
	switch err := atomicfile.ReadJSON(path, &prefs); {
	case err == nil:
		return prefs, nil
	case errors.Is(err, fs.ErrNotExist):
		return TUIPrefs{}, nil
	case errors.Is(err, atomicfile.ErrCorrupt):
		atomicfile.Quarantine(path, "tui preferences file", err)
		return TUIPrefs{}, nil
	default:
		return TUIPrefs{}, fmt.Errorf("failed to read tui preferences: %w", err)
	}
}

// SaveTUIPrefs persists the TUI's preferences atomically.
func SaveTUIPrefs(prefs TUIPrefs) error {
	path, err := statePath(tuiFileName)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteJSON(path, prefs, 0600); err != nil {
		return fmt.Errorf("failed to write tui preferences: %w", err)
	}
	return nil
}
