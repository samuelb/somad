package client

import "golang.org/x/sys/unix"

// socketPeerPID reads the peer's PID through LOCAL_PEERPID.
func socketPeerPID(fd uintptr) (int, error) {
	return unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) // #nosec G115 -- a file descriptor fits an int
}

// socketPeerUID reads the peer's effective UID from its LOCAL_PEERCRED
// credentials.
func socketPeerUID(fd uintptr) (uint32, error) {
	cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED) // #nosec G115 -- a file descriptor fits an int
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
