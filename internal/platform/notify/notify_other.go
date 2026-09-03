//go:build !linux && !darwin

package notify

// Notifier is a no-op on platforms with no supported desktop notification
// backend.
type Notifier struct{}

// New returns a Notifier.
func New() *Notifier {
	return &Notifier{}
}

// Notify does nothing on this platform.
func (n *Notifier) Notify(title, body string) {}
