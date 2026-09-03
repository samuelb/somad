//go:build linux

package notify

import (
	"log"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	notifyDest      = "org.freedesktop.Notifications"
	notifyPath      = "/org/freedesktop/Notifications"
	notifyInterface = "org.freedesktop.Notifications"
)

// Notifier shows desktop notifications over D-Bus
// (org.freedesktop.Notifications.Notify). The session bus connection is
// dialed lazily on the first Notify call, so constructing a Notifier is
// cheap and side-effect free — it never touches D-Bus (and so never fails)
// until a notification is actually due.
type Notifier struct {
	mu       sync.Mutex
	dialOnce sync.Once
	conn     *dbus.Conn
	dialErr  error
	// lastID is the id the notification server returned for the previous
	// notification, passed back as replaces_id so a fast stream of track
	// titles updates one notification bubble instead of stacking a new one
	// per title. 0 (the D-Bus zero value) means "no previous notification".
	lastID uint32
	logged bool
}

// New returns a Notifier. See the type doc: nothing happens until Notify is
// called.
func New() *Notifier {
	return &Notifier{}
}

// Notify shows title as the notification's heading and body as its body,
// replacing this Notifier's previous notification (if any) rather than
// stacking a new one. Failures (no session bus, no notification daemon,
// ...) are logged once and otherwise swallowed: a desktop notification is a
// nice-to-have, never worth taking the daemon down over.
func (n *Notifier) Notify(title, body string) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.dialOnce.Do(func() {
		n.conn, n.dialErr = dbus.ConnectSessionBus()
	})
	if n.dialErr != nil {
		n.logFailureLocked(n.dialErr)
		return
	}

	obj := n.conn.Object(notifyDest, dbus.ObjectPath(notifyPath))
	call := obj.Call(notifyInterface+".Notify", 0,
		"soma",                    // app_name
		n.lastID,                  // replaces_id: 0, or the previous notification's id
		"",                        // app_icon
		title,                     // summary
		body,                      // body
		[]string{},                // actions
		map[string]dbus.Variant{}, // hints
		int32(-1),                 // expire_timeout: server default
	)
	if call.Err != nil {
		n.logFailureLocked(call.Err)
		return
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		n.logFailureLocked(err)
		return
	}
	n.lastID = id
}

// logFailureLocked logs a Notify failure once; caller holds n.mu.
func (n *Notifier) logFailureLocked(err error) {
	if n.logged {
		return
	}
	n.logged = true
	log.Printf("desktop notification failed (further failures are not logged): %v", err)
}
