//go:build !linux

package platform

// MPRIS is a stub for non-Linux platforms.
type MPRIS struct{}

// NewMPRIS returns nil on non-Linux platforms (MPRIS not supported).
func NewMPRIS() (*MPRIS, error) {
	return nil, nil
}

// SetSender is a no-op on non-Linux platforms.
func (m *MPRIS) SetSender(CmdSender) {}

// SetPlaying is a no-op on non-Linux platforms.
func (m *MPRIS) SetPlaying(_, _, _, _ string) {}

// SetStopped is a no-op on non-Linux platforms.
func (m *MPRIS) SetStopped() {}

// SetVolume is a no-op on non-Linux platforms.
func (m *MPRIS) SetVolume(float64) {}

// Close is a no-op on non-Linux platforms.
func (m *MPRIS) Close() {}
