package atomicfile

import (
	"fmt"
	"io"
	"os"
)

// CreateExclusive creates path at perm and hands it to write, but only if
// the file does not exist yet: O_EXCL makes "create only if missing" atomic,
// so a hand-edited file can never be clobbered and concurrent creators
// cannot race each other. It reports created=false, err=nil when the file
// already existed. A failed write removes the partial file so a retry does
// not trip over O_EXCL.
func CreateExclusive(path string, perm os.FileMode, write func(io.Writer) error) (created bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) // #nosec G304 -- callers derive path from the user's own config/flags
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("creating %s: %w", path, err)
	}
	werr := write(f)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("writing %s: %w", path, werr)
	}
	return true, nil
}
