//go:build linux

package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	mprisPath       = "/org/mpris/MediaPlayer2"
	mprisInterface  = "org.mpris.MediaPlayer2"
	playerInterface = "org.mpris.MediaPlayer2.Player"
	busName         = "org.mpris.MediaPlayer2.somatui"
)

// MPRISCmdSender is an interface for sending commands to the application.
// This matches the tea.Program's Send method signature.
type MPRISCmdSender interface {
	Send(msg tea.Msg)
}

// MPRIS handles D-Bus MPRIS integration for desktop media control.
type MPRIS struct {
	conn   *dbus.Conn
	props  *prop.Properties
	sender MPRISCmdSender
}

// mprisRoot implements org.mpris.MediaPlayer2 interface.
type mprisRoot struct {
	mpris *MPRIS
}

// mprisPlayer implements org.mpris.MediaPlayer2.Player interface.
type mprisPlayer struct {
	mpris *MPRIS
}

// NewMPRIS creates a new MPRIS handler and registers it on D-Bus.
func NewMPRIS() (*MPRIS, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to session bus: %w", err)
	}

	m := &MPRIS{
		conn: conn,
	}

	// Request bus name
	reply, err := conn.RequestName(busName, dbus.NameFlagDoNotQueue)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to request bus name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		_ = conn.Close()
		return nil, fmt.Errorf("bus name already taken")
	}

	// Export objects
	root := &mprisRoot{mpris: m}
	player := &mprisPlayer{mpris: m}

	if err := conn.Export(root, mprisPath, mprisInterface); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export root interface: %w", err)
	}
	if err := conn.Export(player, mprisPath, playerInterface); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export player interface: %w", err)
	}

	// Set up properties
	propsSpec := map[string]map[string]*prop.Prop{
		mprisInterface: {
			"CanQuit":             {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanRaise":            {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanSetFullscreen":    {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"DesktopEntry":        {Value: "somatui", Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Fullscreen":          {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"HasTrackList":        {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Identity":            {Value: "SomaUI", Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"SupportedMimeTypes":  {Value: []string{"audio/mpeg"}, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"SupportedUriSchemes": {Value: []string{"http", "https"}, Writable: false, Emit: prop.EmitTrue, Callback: nil},
		},
		playerInterface: {
			"CanControl":     {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanGoNext":      {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanGoPrevious":  {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanPause":       {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanPlay":        {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanSeek":        {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"MaximumRate":    {Value: 1.0, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"MinimumRate":    {Value: 1.0, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"PlaybackStatus": {Value: "Stopped", Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Rate":           {Value: 1.0, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Volume":         {Value: 1.0, Writable: true, Emit: prop.EmitTrue, Callback: nil},
			"Position":       {Value: int64(0), Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Metadata":       {Value: map[string]dbus.Variant{}, Writable: false, Emit: prop.EmitTrue, Callback: nil},
		},
	}

	props, err := prop.Export(conn, mprisPath, propsSpec)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export properties: %w", err)
	}
	m.props = props

	// Export introspection
	introNode := &introspect.Node{
		Name: mprisPath,
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			prop.IntrospectData,
			{
				Name: mprisInterface,
				Methods: []introspect.Method{
					{Name: "Quit"},
					{Name: "Raise"},
				},
				Properties: []introspect.Property{
					{Name: "CanQuit", Type: "b", Access: "read"},
					{Name: "CanRaise", Type: "b", Access: "read"},
					{Name: "CanSetFullscreen", Type: "b", Access: "read"},
					{Name: "DesktopEntry", Type: "s", Access: "read"},
					{Name: "Fullscreen", Type: "b", Access: "read"},
					{Name: "HasTrackList", Type: "b", Access: "read"},
					{Name: "Identity", Type: "s", Access: "read"},
					{Name: "SupportedMimeTypes", Type: "as", Access: "read"},
					{Name: "SupportedUriSchemes", Type: "as", Access: "read"},
				},
			},
			{
				Name: playerInterface,
				Methods: []introspect.Method{
					{Name: "Next"},
					{Name: "Previous"},
					{Name: "Pause"},
					{Name: "PlayPause"},
					{Name: "Stop"},
					{Name: "Play"},
					{Name: "Seek", Args: []introspect.Arg{{Name: "Offset", Type: "x", Direction: "in"}}},
					{Name: "SetPosition", Args: []introspect.Arg{
						{Name: "TrackId", Type: "o", Direction: "in"},
						{Name: "Position", Type: "x", Direction: "in"},
					}},
					{Name: "OpenUri", Args: []introspect.Arg{{Name: "Uri", Type: "s", Direction: "in"}}},
				},
				Properties: []introspect.Property{
					{Name: "CanControl", Type: "b", Access: "read"},
					{Name: "CanGoNext", Type: "b", Access: "read"},
					{Name: "CanGoPrevious", Type: "b", Access: "read"},
					{Name: "CanPause", Type: "b", Access: "read"},
					{Name: "CanPlay", Type: "b", Access: "read"},
					{Name: "CanSeek", Type: "b", Access: "read"},
					{Name: "MaximumRate", Type: "d", Access: "read"},
					{Name: "MinimumRate", Type: "d", Access: "read"},
					{Name: "PlaybackStatus", Type: "s", Access: "read"},
					{Name: "Rate", Type: "d", Access: "read"},
					{Name: "Volume", Type: "d", Access: "readwrite"},
					{Name: "Position", Type: "x", Access: "read"},
					{Name: "Metadata", Type: "a{sv}", Access: "read"},
				},
				Signals: []introspect.Signal{
					{Name: "Seeked", Args: []introspect.Arg{{Name: "Position", Type: "x"}}},
				},
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(introNode), mprisPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export introspectable: %w", err)
	}

	return m, nil
}

// SetSender sets the command sender for MPRIS control messages.
func (m *MPRIS) SetSender(sender MPRISCmdSender) {
	m.sender = sender
}

// SetPlaying updates the playback status to playing and sets metadata.
func (m *MPRIS) SetPlaying(station, track, artist string) {
	if m.props == nil {
		return
	}

	// Sanitize strings to ensure valid UTF8 for D-Bus
	station = sanitizeUTF8(station)
	track = sanitizeUTF8(track)
	artist = sanitizeUTF8(artist)

	metadata := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
		"xesam:title":   dbus.MakeVariant(track),
		"xesam:artist":  dbus.MakeVariant([]string{artist}),
		"xesam:album":   dbus.MakeVariant(station),
	}

	m.props.SetMust(playerInterface, "PlaybackStatus", "Playing")
	m.props.SetMust(playerInterface, "Metadata", metadata)
}

// SetStopped updates the playback status to stopped.
func (m *MPRIS) SetStopped() {
	if m.props == nil {
		return
	}
	m.props.SetMust(playerInterface, "PlaybackStatus", "Stopped")
	m.props.SetMust(playerInterface, "Metadata", map[string]dbus.Variant{})
}

// SetMetadata updates the current track metadata.
func (m *MPRIS) SetMetadata(station, track, artist string) {
	if m.props == nil {
		return
	}

	// Sanitize strings to ensure valid UTF8 for D-Bus
	station = sanitizeUTF8(station)
	track = sanitizeUTF8(track)
	artist = sanitizeUTF8(artist)

	metadata := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/org/mpris/MediaPlayer2/Track/1")),
		"xesam:title":   dbus.MakeVariant(track),
		"xesam:artist":  dbus.MakeVariant([]string{artist}),
		"xesam:album":   dbus.MakeVariant(station),
	}

	m.props.SetMust(playerInterface, "Metadata", metadata)
}

// Close releases D-Bus resources.
func (m *MPRIS) Close() {
	if m.conn != nil {
		_, _ = m.conn.ReleaseName(busName)
		_ = m.conn.Close()
	}
}

// sanitizeUTF8 removes invalid UTF8 characters from a string.
// D-Bus requires all strings to be valid UTF8.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r != utf8.RuneError {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// org.mpris.MediaPlayer2 methods

func (r *mprisRoot) Raise() *dbus.Error {
	return nil
}

func (r *mprisRoot) Quit() *dbus.Error {
	return nil
}

// org.mpris.MediaPlayer2.Player methods

func (p *mprisPlayer) Next() *dbus.Error {
	if p.mpris.sender != nil {
		p.mpris.sender.Send(mprisNextMsg{})
	}
	return nil
}

func (p *mprisPlayer) Previous() *dbus.Error {
	if p.mpris.sender != nil {
		p.mpris.sender.Send(mprisPrevMsg{})
	}
	return nil
}

func (p *mprisPlayer) Pause() *dbus.Error {
	if p.mpris.sender != nil {
		p.mpris.sender.Send(mprisStopMsg{})
	}
	return nil
}

func (p *mprisPlayer) PlayPause() *dbus.Error {
	if p.mpris.sender != nil {
		p.mpris.sender.Send(mprisPlayPauseMsg{})
	}
	return nil
}

func (p *mprisPlayer) Stop() *dbus.Error {
	if p.mpris.sender != nil {
		p.mpris.sender.Send(mprisStopMsg{})
	}
	return nil
}

func (p *mprisPlayer) Play() *dbus.Error {
	if p.mpris.sender != nil {
		p.mpris.sender.Send(mprisPlayMsg{})
	}
	return nil
}

func (p *mprisPlayer) Seek(offset int64, whence int) (int64, error) {
	// D-Bus doesn't support seeking, return appropriate values
	return 0, nil
}

func (p *mprisPlayer) SetPosition(_ dbus.ObjectPath, _ int64) *dbus.Error {
	return nil
}

func (p *mprisPlayer) OpenUri(_ string) *dbus.Error {
	return nil
}
