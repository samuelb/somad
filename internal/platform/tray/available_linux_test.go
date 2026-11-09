//go:build linux

package tray

import (
	"bufio"
	"context"
	"encoding/binary"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// noSessionBus points the session bus at a socket nothing listens on, with
// a display set as on a desktop.
func noSessionBus(t *testing.T) {
	t.Helper()
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "no-bus"))
}

func TestAvailable_FalseWithADisplayButNoSessionBus(t *testing.T) {
	noSessionBus(t)
	if Available() {
		t.Fatal("Available() = true with a display but no session bus to reach, want false")
	}
}

func TestAvailable_TrueWhenTheSessionBusAnswers(t *testing.T) {
	// No display: the bus alone carries the tray.
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+fakeSessionBus(t))
	if !Available() {
		t.Fatal("Available() = false with a session bus that answers, want true")
	}
}

// fakeSessionBus serves just enough of a message bus on a Unix socket for a
// client to connect: the SASL handshake and the reply to Hello. It returns
// the socket's path.
func fakeSessionBus(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bus")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeBus(conn)
		}
	}()
	return path
}

func serveFakeBus(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	in := bufio.NewReader(conn)
	if _, err := in.ReadByte(); err != nil { // the leading NUL
		return
	}
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			return
		}
		var reply string
		switch cmd := strings.TrimSpace(line); {
		case cmd == "AUTH":
			reply = "REJECTED EXTERNAL"
		case strings.HasPrefix(cmd, "AUTH EXTERNAL"):
			reply = "OK 0123456789abcdef0123456789abcdef"
		case cmd == "NEGOTIATE_UNIX_FD":
			reply = "ERROR"
		}
		if reply == "" { // BEGIN
			break
		}
		if _, err := conn.Write([]byte(reply + "\r\n")); err != nil {
			return
		}
	}
	hello, err := dbus.DecodeMessage(in)
	if err != nil {
		return
	}
	_ = (&dbus.Message{
		Type: dbus.TypeMethodReply,
		Headers: map[dbus.HeaderField]dbus.Variant{
			dbus.FieldReplySerial: dbus.MakeVariant(hello.Serial()),
			dbus.FieldSignature:   dbus.MakeVariant(dbus.SignatureOf("")),
		},
		Body: []any{":1.1"},
	}).EncodeTo(conn, binary.LittleEndian)
	// Hold the connection until the client closes it.
	_, _ = in.ReadByte()
}

// With no bus to set itself up on, systray leaves its connection nil and
// closes it at quit all the same; that must not take the process down with
// it when a tray is run regardless (Available is the caller's check).
func TestRunAndQuit_SurviveATrayThatNeverReachedTheBus(t *testing.T) {
	noSessionBus(t)
	tr := New()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		tr.Run(nil)
	}()
	tr.Quit()
	select {
	case panicked := <-done:
		if panicked != nil {
			t.Fatalf("Run panicked at quit: %v", panicked)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after Quit")
	}
	tr.Quit() // and again, after Run is gone
	tr.SetPlaying("groovesalad", "Groove Salad", "A Track")
}
