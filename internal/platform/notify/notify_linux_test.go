//go:build linux

package notify

import (
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyArgs_DropsInvalidUTF8(t *testing.T) {
	// ICY metadata is often Latin-1: "Café" arrives as "Caf\xe9".
	args := notifyArgs(7, "Caf\xe9 Del Mar", "Artist \xff· Channel", false)

	assert.Equal(t, uint32(7), args[1], "replaces_id")
	assert.Equal(t, "Caf Del Mar", args[3], "summary")
	assert.Equal(t, "Artist · Channel", args[4], "body")

	// godbus refuses to encode a string that is not valid UTF-8, which
	// would lose the notification; the call must now encode.
	msg := &dbus.Message{
		Type: dbus.TypeMethodCall,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldPath:        dbus.MakeVariant(dbus.ObjectPath(notifyPath)),
			dbus.FieldInterface:   dbus.MakeVariant(notifyInterface),
			dbus.FieldMember:      dbus.MakeVariant("Notify"),
			dbus.FieldDestination: dbus.MakeVariant(notifyDest),
			dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf(args...)),
		},
		Body: args,
	}
	require.NoError(t, msg.IsValid())
}

func TestEscapeMarkup(t *testing.T) {
	for text, want := range map[string]string{
		"Simon & Garfunkel · Left Coast 70s":   "Simon &amp; Garfunkel · Left Coast 70s",
		"<3 · Groove Salad":                    "&lt;3 · Groove Salad",
		`<a href="https://example.com">x</a>`:  `&lt;a href="https://example.com"&gt;x&lt;/a&gt;`,
		`<img src="file:///x.png" alt="a"/> y`: `&lt;img src="file:///x.png" alt="a"/&gt; y`,
		"already &amp; escaped":                "already &amp;amp; escaped",
		`Don't "Stop" · Drone Zone`:            `Don't "Stop" · Drone Zone`,
		"":                                     "",
	} {
		assert.Equal(t, want, escapeMarkup(text), "%q", text)
	}
}

func TestNotifyArgs_EscapesTheBodyOnlyAndOnlyForMarkup(t *testing.T) {
	plain := notifyArgs(0, "AT&T <live>", "Simon & Garfunkel · <3 FM", false)
	assert.Equal(t, "AT&T <live>", plain[3])
	assert.Equal(t, "Simon & Garfunkel · <3 FM", plain[4], "a server without markup shows the body as sent")

	markup := notifyArgs(0, "AT&T <live>", "Simon & Garfunkel · <3 FM", true)
	assert.Equal(t, "AT&T <live>", markup[3], "the summary is never markup")
	assert.Equal(t, "Simon &amp; Garfunkel · &lt;3 FM", markup[4])
}

// fakeServer stands in for the notification server's object on the bus:
// it answers GetCapabilities and Notify and records the calls.
type fakeServer struct {
	dbus.BusObject // only Call is used

	caps    []string
	capsErr error
	calls   []string
	sent    [][]any // the arguments of every Notify
	lastID  uint32  // the id of the last notification, 41 for the first
}

func (f *fakeServer) Call(method string, _ dbus.Flags, args ...any) *dbus.Call {
	f.calls = append(f.calls, method)
	switch method {
	case notifyInterface + ".GetCapabilities":
		return &dbus.Call{Err: f.capsErr, Body: []any{f.caps}}
	case notifyInterface + ".Notify":
		f.sent = append(f.sent, args)
		f.lastID = max(f.lastID, 40) + 1
		return &dbus.Call{Body: []any{f.lastID}}
	}
	return &dbus.Call{Err: errors.New("unknown method " + method)}
}

// notifierOn returns a Notifier connected to server.
func notifierOn(server *fakeServer) *Notifier {
	n := New()
	n.dialOnce.Do(func() {}) // the dial is done
	n.server = server
	return n
}

func TestNotify_EscapesTheBodyForAServerWithBodyMarkup(t *testing.T) {
	server := &fakeServer{caps: []string{"actions", "body", "body-markup", "persistence"}}
	n := notifierOn(server)

	n.Notify("R&B <Classics>", `Earth, Wind & Fire <a href="https://example.com">x</a> · Seven Inch Soul`)

	require.Len(t, server.sent, 1)
	assert.Equal(t, "R&B <Classics>", server.sent[0][3], "the summary is plain text to every server")
	assert.Equal(t, `Earth, Wind &amp; Fire &lt;a href="https://example.com"&gt;x&lt;/a&gt; · Seven Inch Soul`, server.sent[0][4])
}

func TestNotify_LeavesTheBodyAloneForAServerWithoutBodyMarkup(t *testing.T) {
	server := &fakeServer{caps: []string{"actions", "body"}}
	n := notifierOn(server)

	n.Notify("R&B <Classics>", "Earth, Wind & Fire · Seven Inch Soul")

	require.Len(t, server.sent, 1)
	assert.Equal(t, "Earth, Wind & Fire · Seven Inch Soul", server.sent[0][4], "entities would show as written")
}

func TestNotify_AsksForTheCapabilitiesOnce(t *testing.T) {
	server := &fakeServer{caps: []string{"body-markup"}}
	n := notifierOn(server)

	n.Notify("One", "A & B")
	n.Notify("Two", "C & D")

	assert.Equal(t, []string{
		notifyInterface + ".GetCapabilities",
		notifyInterface + ".Notify",
		notifyInterface + ".Notify",
	}, server.calls)
	require.Len(t, server.sent, 2)
	assert.Equal(t, uint32(0), server.sent[0][1])
	assert.Equal(t, uint32(41), server.sent[1][1], "the second replaces the first")
	assert.Equal(t, "C &amp; D", server.sent[1][4])
}

func TestNotify_SendsNothingUntilTheCapabilitiesAreKnown(t *testing.T) {
	server := &fakeServer{capsErr: errors.New("org.freedesktop.DBus.Error.ServiceUnknown")}
	n := notifierOn(server)

	n.Notify("One", "<b>A</b>")
	assert.Empty(t, server.sent, "the body may be markup to this server: not sent as it is")

	// The notification server is up for the next track.
	server.capsErr, server.caps = nil, []string{"body-markup"}
	n.Notify("Two", "<b>A</b>")
	require.Len(t, server.sent, 1)
	assert.Equal(t, "&lt;b&gt;A&lt;/b&gt;", server.sent[0][4])
}
