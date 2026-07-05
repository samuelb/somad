// Package atomicfile writes files atomically via a temp file and rename, so
// a crash or power loss mid-write never leaves a truncated or corrupt file
// behind.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically: the data goes to a temp file in
// the same directory (so the rename stays on one filesystem), is synced, and
// is renamed over path. Readers see either the old content or the new one,
// never a partial write.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name()) // no-op after a successful rename
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("setting temp file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("syncing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", filepath.Base(path), err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("syncing parent directory: %w", err)
	}
	return nil
}

func syncDir(dir string) error {
	f, err := os.Open(dir) // #nosec G304 -- directory is derived from caller-provided output path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
