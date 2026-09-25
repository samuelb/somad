package atomicfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// CreateExclusive creates path at perm with the content write produces, but
// only if the file does not exist yet, so a hand-edited file can never be
// clobbered and concurrent creators cannot race each other. It reports
// created=false, err=nil when the file already existed.
//
// Like WriteFile, the content goes to a synced temp file first, which is
// then hard-linked into place: a link, unlike a rename, fails when path
// exists, keeping "create only if missing" atomic. A crash therefore leaves
// either no file or the complete one, never the empty file an in-place
// write can leave, which for a generated PSK would stop the daemon from
// starting until the file was deleted by hand.
func CreateExclusive(path string, perm os.FileMode, write func(io.Writer) error) (created bool, err error) {
	// The common case (the config template on every daemon start) is an
	// existing file; don't write and sync a temp file just to discard it.
	if _, err := os.Lstat(path); err == nil {
		return false, nil
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return false, fmt.Errorf("creating %s: %w", path, err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name()) // path keeps its own link after success
	}()

	if err := tmp.Chmod(perm); err != nil {
		return false, fmt.Errorf("creating %s: setting permissions: %w", path, err)
	}
	if err := write(tmp); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("creating %s: %w", path, err)
	}
	if err := syncDir(dir); err != nil {
		return true, fmt.Errorf("creating %s: syncing parent directory: %w", path, err)
	}
	return true, nil
}
