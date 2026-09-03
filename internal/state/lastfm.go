package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"somad/internal/atomicfile"
)

// lastfmFileName is kept separate from state.json (ADR-0012's sequence-
// ordered saves) and, deliberately, from the config file: "soma lastfm
// login" must never edit a user's hand-written config.yaml (ADR-0011).
const lastfmFileName = "lastfm.json"

// LastfmSession is the persisted outcome of "soma lastfm login".
type LastfmSession struct {
	SessionKey string `json:"session_key"`
}

// lastfmSessionFilePath returns the path to lastfm.json, creating the state
// directory if needed.
func lastfmSessionFilePath() (string, error) {
	return statePath(lastfmFileName)
}

// LoadLastfmSession reads the persisted Last.fm session key. A missing file
// is not an error and yields an empty key (not logged in yet). A corrupt
// file is quarantined the same way state.json is (ADR-0012: user-written
// files fail loudly, machine-written ones are moved aside) and treated as
// missing.
func LoadLastfmSession() (string, error) {
	path, err := lastfmSessionFilePath()
	if err != nil {
		return "", err
	}
	var sess LastfmSession
	switch err := atomicfile.ReadJSON(path, &sess); {
	case err == nil:
		return sess.SessionKey, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case errors.Is(err, atomicfile.ErrCorrupt):
		atomicfile.Quarantine(path, "last.fm session file", err)
		return "", nil
	default:
		return "", fmt.Errorf("failed to read last.fm session file: %w", err)
	}
}

// SaveLastfmSession persists the session key obtained by "soma lastfm
// login", atomically and at 0600: it is a bearer credential for the user's
// Last.fm account, like the PSK files this codebase already protects the
// same way.
func SaveLastfmSession(sessionKey string) error {
	path, err := lastfmSessionFilePath()
	if err != nil {
		return err
	}
	if err := atomicfile.WriteJSON(path, LastfmSession{SessionKey: sessionKey}, 0600); err != nil { // #nosec G117 -- this file *is* the credential store, written at 0600
		return fmt.Errorf("failed to write last.fm session file: %w", err)
	}
	return nil
}

// ClearLastfmSession removes the persisted session ("soma lastfm logout").
// Removing an already-absent file is not an error.
func ClearLastfmSession() error {
	path, err := lastfmSessionFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove last.fm session file: %w", err)
	}
	return nil
}
