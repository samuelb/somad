//go:build !darwin && !linux

package client

import "errors"

// socketPeerPID is unsupported here: soma targets Linux and macOS, and other
// systems spell peer credentials differently.
func socketPeerPID(uintptr) (int, error) {
	return 0, errors.New("reading a Unix socket's peer process is not supported on this platform")
}
