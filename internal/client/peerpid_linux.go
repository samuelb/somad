package client

import "golang.org/x/sys/unix"

// socketPeerPID reads the peer's PID from its SO_PEERCRED credentials.
func socketPeerPID(fd uintptr) (int, error) {
	cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) // #nosec G115 -- a file descriptor fits an int
	if err != nil {
		return 0, err
	}
	return int(cred.Pid), nil
}
