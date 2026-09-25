package client

import (
	"errors"
	"net"
)

// peerPID returns the process ID of the server at the other end of a Unix
// socket connection, as the kernel recorded it when the connection was made.
// It is what lets a client stop a local daemon that refuses every request
// (see ProtocolSkewError). socketPeerPID is per platform.
func peerPID(nc net.Conn) (int, error) {
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a Unix socket connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var pidErr error
	if err := raw.Control(func(fd uintptr) { pid, pidErr = socketPeerPID(fd) }); err != nil {
		return 0, err
	}
	if pidErr != nil {
		return 0, pidErr
	}
	if pid <= 0 {
		return 0, errors.New("the kernel reported no peer process")
	}
	return pid, nil
}
