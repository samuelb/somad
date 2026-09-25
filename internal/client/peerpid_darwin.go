package client

import "golang.org/x/sys/unix"

// socketPeerPID reads the peer's PID through LOCAL_PEERPID.
func socketPeerPID(fd uintptr) (int, error) {
	return unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) // #nosec G115 -- a file descriptor fits an int
}
