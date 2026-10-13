package client

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// ErrForeignDaemon reports a local socket served by another user's process.
// The fallback socket path ($TMPDIR/somad-<uid>/somad.sock, used when
// $XDG_RUNTIME_DIR is unset) is predictable, and on Linux $TMPDIR is
// usually the world-writable /tmp, so another local user can claim the
// directory first and serve a fake daemon there, one that sees every
// command and answers with whatever state it likes. The real daemon then
// refuses to listen in a directory it does not own (ADR-0007), but the
// client dials whatever is at the path, so it checks who it reached
// (verifyPeerOwner).
var ErrForeignDaemon = errors.New("the soma daemon socket is served by another user")

// errPeerCredUnsupported is what the socketPeer* functions return where
// there is no implementation (peercred_other.go).
var errPeerCredUnsupported = errors.New("reading a Unix socket's peer credentials is not supported on this platform")

// peerUID returns the user ID of the process at the other end of a Unix
// socket connection, as the kernel recorded it when the connection was
// made. A variable so tests, whose fake daemons run as the test's own user,
// can play one run by somebody else. socketPeerUID is per platform.
var peerUID = func(nc net.Conn) (uint32, error) {
	var uid uint32
	err := controlUnixConn(nc, func(fd uintptr) (err error) {
		uid, err = socketPeerUID(fd)
		return err
	})
	return uid, err
}

// verifyPeerOwner refuses a local daemon run by another user (see
// ErrForeignDaemon). On Linux and macOS, where the kernel reports the peer's
// credentials, a failure to read them refuses the connection too; elsewhere
// (soma targets only those two) the check is skipped and the socket
// directory's permissions are all there is, as before it existed.
func verifyPeerOwner(nc net.Conn, ep Endpoint) error {
	uid, err := peerUID(nc)
	if errors.Is(err, errPeerCredUnsupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot tell who serves the soma daemon socket %s: %w", ep, err)
	}
	if me := os.Getuid(); int64(uid) != int64(me) {
		return fmt.Errorf("%w: %s belongs to uid %d, not to you (uid %d), so it is not your daemon; "+
			"point $XDG_RUNTIME_DIR or $SOMAD_SOCKET at a directory only you can write to", ErrForeignDaemon, ep, uid, me)
	}
	return nil
}

// peerPID returns the process ID of the server at the other end of a Unix
// socket connection, as the kernel recorded it when the connection was made.
// It is what lets a client stop a local daemon that refuses every request
// (see ProtocolSkewError). socketPeerPID is per platform.
func peerPID(nc net.Conn) (int, error) {
	var pid int
	err := controlUnixConn(nc, func(fd uintptr) (err error) {
		pid, err = socketPeerPID(fd)
		return err
	})
	if err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, errors.New("the kernel reported no peer process")
	}
	return pid, nil
}

// controlUnixConn runs read on the descriptor of a Unix socket connection,
// for the peer credential lookups.
func controlUnixConn(nc net.Conn, read func(fd uintptr) error) error {
	uc, ok := nc.(*net.UnixConn)
	if !ok {
		return errors.New("not a Unix socket connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var readErr error
	if err := raw.Control(func(fd uintptr) { readErr = read(fd) }); err != nil {
		return err
	}
	return readErr
}
