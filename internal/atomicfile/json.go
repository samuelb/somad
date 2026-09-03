package atomicfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
)

// ErrCorrupt marks a file that exists but does not decode. Callers of
// ReadJSON test for it with errors.Is to decide between quarantining the
// file (machine-written state, ADR-0012) and failing loudly (user-written
// config, ADR-0011).
var ErrCorrupt = errors.New("corrupt JSON file")

// ReadJSON decodes the JSON file at path into v. A missing file yields an
// error satisfying errors.Is(err, fs.ErrNotExist); a file that exists but
// does not parse yields one satisfying errors.Is(err, ErrCorrupt).
func ReadJSON(path string, v any) error {
	data, err := os.ReadFile(path) // #nosec G304 -- callers derive path from XDG dirs, not user input
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	return nil
}

// WriteJSON marshals v (indented) and writes it atomically at perm.
func WriteJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling: %w", err)
	}
	return WriteFile(path, data, perm)
}

// Quarantine moves a corrupt machine-written file aside as path+".corrupt"
// so the next save does not destroy the evidence, and logs what happened.
// label names the file in the log line ("state file", "channel cache").
func Quarantine(path, label string, cause error) {
	backupPath := path + ".corrupt"
	if err := os.Rename(path, backupPath); err != nil {
		log.Printf("warning: %s is corrupt (%v) and could not be moved aside: %v", label, cause, err)
		return
	}
	log.Printf("warning: %s is corrupt (%v), moved to %s, starting fresh", label, cause, backupPath)
}
