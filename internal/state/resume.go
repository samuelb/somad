package state

import (
	"fmt"
	"os"
	"time"

	"somad/internal/atomicfile"
)

// resumeFileName is the note a daemon leaves for the process started in its
// place when it restarts itself (ADR-0037). It is a file because nothing
// else reaches that process in both of the ways it can be started: a
// daemon that re-executes itself could hand it over in the environment, but
// one that exits for its service manager to start again (ADR-0038) cannot.
const resumeFileName = "resume.json"

// Resume is what a daemon that restarts itself hands to its successor:
// the channel a user asked it to play, which it could not, and its pending
// sleep timer, which lives in no other place.
type Resume struct {
	ChannelID string `json:"channel_id"`
	// StopAt is when the pending sleep timer stops playback; zero when
	// none was pending.
	StopAt time.Time `json:"stop_at,omitzero"`
	// At is when the note was written; see TakeResume.
	At time.Time `json:"at"`
}

// SaveResume writes the note for the next daemon to start, stamping it
// with the current time.
func SaveResume(r Resume) error {
	path, err := statePath(resumeFileName)
	if err != nil {
		return err
	}
	r.At = time.Now()
	if err := atomicfile.WriteJSON(path, r, 0600); err != nil {
		return fmt.Errorf("failed to write the resume file: %w", err)
	}
	return nil
}

// TakeResume returns the note a daemon left when it restarted itself, and
// removes it: it is for one start only. A note older than maxAge is not
// returned. The restart it was written for never happened then (the
// service manager did not start the daemon again, the machine went down),
// and a daemon started for some other reason, hours later, must not begin
// to play on its own. Nor is one from the future, written before the clock
// was set back. A missing or unreadable note is no note.
func TakeResume(maxAge time.Duration) (Resume, bool) {
	path, err := statePath(resumeFileName)
	if err != nil {
		return Resume{}, false
	}
	var r Resume
	readErr := atomicfile.ReadJSON(path, &r)
	// Removed whatever it held: a note that could not be read now will not
	// read better later, and by then it would be stale besides.
	if err := os.Remove(path); err != nil {
		// Not there (the usual case), or it cannot be removed, and would
		// then be taken again at the next start: better not to resume.
		return Resume{}, false
	}
	if readErr != nil || r.ChannelID == "" {
		return Resume{}, false
	}
	if age := time.Since(r.At); age < 0 || age > maxAge {
		return Resume{}, false
	}
	return r, true
}
