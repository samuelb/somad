package state

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
	stateDir, err := getStateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create state directory: %w", err)
	}
	return filepath.Join(stateDir, lastfmFileName), nil
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

	data, err := os.ReadFile(path) // #nosec G304 -- path derived from the state dir, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read last.fm session file: %w", err)
	}

	var sess LastfmSession
	if err := json.Unmarshal(data, &sess); err != nil {
		backupPath := path + ".corrupt"
		if renameErr := os.Rename(path, backupPath); renameErr != nil {
			log.Printf("warning: last.fm session file is corrupt (%v) and could not be moved aside: %v", err, renameErr)
		} else {
			log.Printf("warning: last.fm session file is corrupt (%v), moved to %s, starting fresh", err, backupPath)
		}
		return "", nil
	}
	return sess.SessionKey, nil
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
	data, err := json.MarshalIndent(LastfmSession{SessionKey: sessionKey}, "", "  ") // #nosec G117 -- this file *is* the credential store, written at 0600 below
	if err != nil {
		return fmt.Errorf("failed to marshal last.fm session: %w", err)
	}
	if err := atomicfile.WriteFile(path, data, 0600); err != nil {
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
