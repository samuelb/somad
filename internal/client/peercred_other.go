//go:build !darwin && !linux

package client

// socketPeerPID is unsupported here: soma targets Linux and macOS, and other
// systems spell peer credentials differently.
func socketPeerPID(uintptr) (int, error) {
	return 0, errPeerCredUnsupported
}

// socketPeerUID is unsupported here, for the same reason; verifyPeerOwner
// skips its check.
func socketPeerUID(uintptr) (uint32, error) {
	return 0, errPeerCredUnsupported
}
