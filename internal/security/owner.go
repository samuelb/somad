package security

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// CheckOwnerOnly rejects a file or directory that is readable by group or
// others, or that is owned by a different user than the one running soma:
// the SSH-style check applied to anything that grants control over the
// daemon (the socket directory, a PSK file). what names the item in error
// messages, e.g. "PSK file /home/me/psk".
func CheckOwnerOnly(info fs.FileInfo, what string) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must not be accessible by group or others", what)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("could not inspect owner of %s", what)
	}
	uid := os.Getuid()
	if uid < 0 || uid > int(^uint32(0)) {
		return fmt.Errorf("current uid %d cannot be represented for %s owner check", uid, what)
	}
	currentUID := uint32(uid)
	if st.Uid != currentUID {
		return fmt.Errorf("%s is owned by uid %d, not current uid %d", what, st.Uid, currentUID)
	}
	return nil
}
