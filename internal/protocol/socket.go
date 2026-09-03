package protocol

import (
	"fmt"
	"os"
	"path/filepath"

	"somad/internal/security"
)

// SocketPath returns the Unix socket path shared by client and server.
// Resolution order: $SOMAD_SOCKET override, $XDG_RUNTIME_DIR, then a
// per-user directory under the OS temp dir. Kept short deliberately —
// sun_path is capped at 104 bytes on macOS.
func SocketPath() string {
	if p := os.Getenv("SOMAD_SOCKET"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "somad.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("somad-%d", os.Getuid()), "somad.sock")
}

// LockPath returns the server's single-instance lock file, kept next to the
// socket.
func LockPath(socketPath string) string {
	return socketPath + ".lock"
}

// EnsureSocketDir creates the socket's parent directory (user-only
// permissions) if it does not exist yet.
func EnsureSocketDir(socketPath string) error {
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("socket parent is not a directory: %s", dir)
	}
	return security.CheckOwnerOnly(info, "socket directory "+dir)
}
