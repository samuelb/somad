//go:build linux

package notify

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyArgs_DropsInvalidUTF8(t *testing.T) {
	// ICY metadata is often Latin-1: "Café" arrives as "Caf\xe9".
	args := notifyArgs(7, "Caf\xe9 Del Mar", "Artist \xff· Channel")

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
