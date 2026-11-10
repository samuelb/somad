//go:build linux

package notify

import (
	"slices"
	"strings"
	"sync"

	"somad/internal/platform"

	"github.com/godbus/dbus/v5"
)

const (
	notifyDest      = "org.freedesktop.Notifications"
	notifyPath      = "/org/freedesktop/Notifications"
	notifyInterface = "org.freedesktop.Notifications"
	// capBodyMarkup is the capability of a notification server that reads
	// the body as markup.
	capBodyMarkup = "body-markup"
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
	// server is the notification server's object on the bus, from the dial
	// on.
	server dbus.BusObject
	// bodyMarkup is whether the server reads the body as markup, as its
	// GetCapabilities said; capsKnown is false until it has answered.
	capsKnown  bool
	bodyMarkup bool
	// lastID is the id the notification server returned for the previous
	// notification, passed back as replaces_id so a fast stream of track
	// titles updates one notification bubble instead of stacking a new one
	// per title. 0 (the D-Bus zero value) means "no previous notification".
	lastID   uint32
	failures failureLog
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
		if n.conn, n.dialErr = dbus.ConnectSessionBus(); n.dialErr == nil {
			n.server = n.conn.Object(notifyDest, dbus.ObjectPath(notifyPath))
		}
	})
	if n.dialErr != nil {
		n.failures.log(n.dialErr)
		return
	}

	if !n.capsKnown {
		// Asked once. Without an answer nothing is sent, and the next
		// notification asks again: a body sent as it is to a server that
		// turns out to read markup is the mistake this is here to prevent.
		var caps []string
		if err := n.server.Call(notifyInterface+".GetCapabilities", 0).Store(&caps); err != nil {
			n.failures.log(err)
			return
		}
		n.capsKnown, n.bodyMarkup = true, slices.Contains(caps, capBodyMarkup)
	}

	call := n.server.Call(notifyInterface+".Notify", 0, notifyArgs(n.lastID, title, body, n.bodyMarkup)...)
	if call.Err != nil {
		n.failures.log(call.Err)
		return
	}
	var id uint32
	if err := call.Store(&id); err != nil {
		n.failures.log(err)
		return
	}
	n.lastID = id
}

// notifyArgs returns the arguments of an org.freedesktop.Notifications.Notify
// call. title and body come from ICY metadata, which can carry any bytes,
// but D-Bus strings must be valid UTF-8 and godbus refuses to send a message
// that breaks this. That would lose the notification and use up the
// one-time failure log, so invalid sequences are dropped here. The body is
// escaped for a server that reads it as markup (bodyMarkup); the summary is
// plain text to every server.
func notifyArgs(replacesID uint32, title, body string, bodyMarkup bool) []any {
	body = platform.SanitizeUTF8(body)
	if bodyMarkup {
		body = escapeMarkup(body)
	}
	return []any{
		"soma",                       // app_name
		replacesID,                   // replaces_id: 0, or the previous notification's id
		"",                           // app_icon
		platform.SanitizeUTF8(title), // summary
		body,                         // body
		[]string{},                   // actions
		map[string]dbus.Variant{},    // hints
		int32(-1),                    // expire_timeout: server default
	}
}

// markupEscaper writes the characters that start markup as entities.
var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeMarkup returns text as a notification body that a server with the
// body-markup capability shows as written. Such a server parses the body:
// an artist called "<3" or "Simon & Garfunkel" lost text, and a title from
// the stream could carry the <a href> and <img> tags some servers honor.
// Only the three characters that start a tag or an entity are replaced, by
// the entities every markup parser knows; html.EscapeString would also turn
// quotes into numeric ones, which not every server reads.
func escapeMarkup(text string) string {
	return markupEscaper.Replace(text)
}
