package protocol

import (
	"fmt"
	"os"
	"path/filepath"
)

// SocketPath returns the Unix socket path shared by client and server.
// Resolution order: $SOMATUI_SOCKET override, $XDG_RUNTIME_DIR, then a
// per-user directory under the OS temp dir. Kept short deliberately —
// sun_path is capped at 104 bytes on macOS.
func SocketPath() string {
	if p := os.Getenv("SOMATUI_SOCKET"); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "somatui.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("somatui-%d", os.Getuid()), "somatui.sock")
}

// LockPath returns the server's single-instance lock file, kept next to the
// socket.
func LockPath(socketPath string) string {
	return socketPath + ".lock"
}

// EnsureSocketDir creates the socket's parent directory (user-only
// permissions) if it does not exist yet.
func EnsureSocketDir(socketPath string) error {
	return os.MkdirAll(filepath.Dir(socketPath), 0o700)
}
