//go:build !linux && !darwin

package platform

// MPRIS is a stub for platforms with neither MPRIS (Linux) nor Now Playing
// (macOS).
type MPRIS struct{}

// NewMPRIS returns nil: nothing here plays MPRIS's part.
func NewMPRIS() (*MPRIS, error) {
	return nil, nil
}

// SetSender is a no-op here.
func (m *MPRIS) SetSender(CmdSender) {}

// SetPlaying is a no-op here.
func (m *MPRIS) SetPlaying(_, _, _, _ string) {}

// SetStopped is a no-op here.
func (m *MPRIS) SetStopped() {}

// SetVolume is a no-op here.
func (m *MPRIS) SetVolume(float64) {}

// Close is a no-op here.
func (m *MPRIS) Close() {}

// NeedsMainLoop is always false here.
func (m *MPRIS) NeedsMainLoop() bool { return false }

// RunMainLoop is a no-op here.
func (m *MPRIS) RunMainLoop() {}

// StopMainLoop is a no-op here.
func (m *MPRIS) StopMainLoop() {}
