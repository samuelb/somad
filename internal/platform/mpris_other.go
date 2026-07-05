//go:build !linux

package platform

// CmdSender is an interface for sending commands to the application.
// This matches the tea.Program's Send method signature (tea.Msg is any).
type CmdSender interface {
	Send(msg any)
}

// MPRIS is a stub for non-Linux platforms.
type MPRIS struct{}

// NewMPRIS returns nil on non-Linux platforms (MPRIS not supported).
func NewMPRIS() (*MPRIS, error) {
	return nil, nil
}

// SetSender is a no-op on non-Linux platforms.
func (m *MPRIS) SetSender(sender CmdSender) {}

// SetPlaying is a no-op on non-Linux platforms.
func (m *MPRIS) SetPlaying(station, track, artist string) {}

// SetStopped is a no-op on non-Linux platforms.
func (m *MPRIS) SetStopped() {}

// SetMetadata is a no-op on non-Linux platforms.
func (m *MPRIS) SetMetadata(station, track, artist string) {}

// Close is a no-op on non-Linux platforms.
func (m *MPRIS) Close() {}

// MPRISPlayMsg is sent when MPRIS requests to play.
type MPRISPlayMsg struct{}

// MPRISStopMsg is sent when MPRIS requests to stop.
type MPRISStopMsg struct{}

// MPRISPlayPauseMsg is sent when MPRIS requests to toggle play/pause.
type MPRISPlayPauseMsg struct{}

// MPRISNextMsg is sent when MPRIS requests to go to next track.
type MPRISNextMsg struct{}

// MPRISPrevMsg is sent when MPRIS requests to go to previous track.
type MPRISPrevMsg struct{}

// MPRISVolumeMsg is sent when MPRIS requests a volume change.
type MPRISVolumeMsg struct {
	Volume float64
}

// SetVolume is a no-op on non-Linux platforms.
func (m *MPRIS) SetVolume(v float64) {}

// SanitizeUTF8 is a no-op on non-Linux platforms.
func SanitizeUTF8(s string) string { return s }
