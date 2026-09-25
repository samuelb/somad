//go:build linux

package platform

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	mprisPath       = "/org/mpris/MediaPlayer2"
	mprisInterface  = "org.mpris.MediaPlayer2"
	playerInterface = "org.mpris.MediaPlayer2.Player"
	busName         = "org.mpris.MediaPlayer2.soma"

	// trackIDPrefix is where the mpris:trackid object paths live. The spec
	// reserves /org/mpris for itself, so they sit under the project's own
	// namespace instead.
	trackIDPrefix = "/io/github/samuelb/somad/track/"
)

// playerMethodNames maps mprisPlayer's Go method names to the D-Bus names
// they are exported under where the two differ: a Go method named Seek must
// have io.Seeker's signature (go vet), which MPRIS's Seek does not.
var playerMethodNames = map[string]string{"SeekOffset": "Seek"}

// MPRIS handles D-Bus MPRIS integration for desktop media control.
type MPRIS struct {
	conn  *dbus.Conn
	props *prop.Properties

	// senderMu guards sender: D-Bus method handlers read it from godbus
	// goroutines while SetSender is called after the bus objects are already
	// exported.
	senderMu sync.Mutex
	sender   CmdSender

	// closed is set by Close. The server closes MPRIS off its lock during
	// shutdown, so a request still being served can reach the setters after
	// the bus is gone; they must become no-ops rather than fail.
	closed atomic.Bool

	// trackMu guards trackKey and trackSeq, the track the current
	// mpris:trackid names and its number; see trackID.
	trackMu  sync.Mutex
	trackKey string
	trackSeq uint64
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
	if err := conn.ExportWithMap(player, playerMethodNames, mprisPath, playerInterface); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export player interface: %w", err)
	}

	props, err := prop.Export(conn, mprisPath, m.propsSpec())
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export properties: %w", err)
	}
	m.props = props

	if err := conn.Export(introspect.NewIntrospectable(introspectNode()), mprisPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to export introspectable: %w", err)
	}

	return m, nil
}

// propsSpec returns the exported properties of both MPRIS interfaces with
// their initial values.
func (m *MPRIS) propsSpec() map[string]map[string]*prop.Prop {
	return map[string]map[string]*prop.Prop{
		mprisInterface: {
			"CanQuit":          {Value: true, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanRaise":         {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"CanSetFullscreen": {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"DesktopEntry":     {Value: "soma", Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Fullscreen":       {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"HasTrackList":     {Value: false, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Identity":         {Value: "Soma", Writable: false, Emit: prop.EmitTrue, Callback: nil},
			// OpenUri does nothing (only SomaFM channels play), so no URI
			// scheme or MIME type is advertised as openable.
			"SupportedMimeTypes":  {Value: []string{}, Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"SupportedUriSchemes": {Value: []string{}, Writable: false, Emit: prop.EmitTrue, Callback: nil},
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
			"Volume":         {Value: 1.0, Writable: true, Emit: prop.EmitTrue, Callback: m.onVolumeChange},
			"Position":       {Value: int64(0), Writable: false, Emit: prop.EmitTrue, Callback: nil},
			"Metadata":       {Value: map[string]dbus.Variant{}, Writable: false, Emit: prop.EmitTrue, Callback: nil},
		},
	}
}

// introspectNode describes the exported object for introspection. Every
// method listed must also be one godbus exports on mprisRoot or mprisPlayer
// (see TestMPRIS_IntrospectedMethodsAreExported), or callers that trust it
// get UnknownMethod.
func introspectNode() *introspect.Node {
	return &introspect.Node{
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
}

// SetSender sets the command sender for MPRIS control messages.
func (m *MPRIS) SetSender(sender CmdSender) {
	m.senderMu.Lock()
	defer m.senderMu.Unlock()
	m.sender = sender
}

// send forwards a control message to the current sender, if any.
func (m *MPRIS) send(msg any) {
	m.senderMu.Lock()
	sender := m.sender
	m.senderMu.Unlock()
	if sender != nil {
		sender.Send(msg)
	}
}

// SetPlaying updates the playback status to playing and sets metadata.
// artURL is the channel's artwork URL (mpris:artUrl), or "" when the channel
// has none.
func (m *MPRIS) SetPlaying(station, track, artist, artURL string) {
	if m.props == nil {
		return
	}

	m.setProp("PlaybackStatus", "Playing")
	m.setProp("Metadata", buildMetadata(m.trackID(station, track, artist), station, track, artist, artURL))
}

// trackID returns the mpris:trackid for a track: the same object path for
// as long as the same track is reported, and a new one whenever it changes,
// which is how clients tell tracks apart.
func (m *MPRIS) trackID(station, track, artist string) dbus.ObjectPath {
	key := station + "\x00" + track + "\x00" + artist
	m.trackMu.Lock()
	defer m.trackMu.Unlock()
	if key != m.trackKey {
		m.trackKey = key
		m.trackSeq++
	}
	return dbus.ObjectPath(trackIDPrefix + strconv.FormatUint(m.trackSeq, 10))
}

// setProp mirrors one player property to the bus. Emitting the
// PropertiesChanged signal fails once the connection is closed, whether
// by Close or by a session bus that died under a long-running daemon, and
// prop.SetMust turns that failure into a panic that would take the whole
// daemon down. It is recovered into a log line here instead; a closed MPRIS
// skips the update altogether. SetMust (not Set) is deliberate: Set runs
// the property's write callback, which for Volume would feed the daemon's
// own volume change back to it.
func (m *MPRIS) setProp(name string, v any) {
	if m.props == nil || m.closed.Load() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("mpris: updating %s failed: %v", name, r)
		}
	}()
	m.props.SetMust(playerInterface, name, v)
}

// buildMetadata assembles the MPRIS Metadata property for a playing track
// named trackID. artURL is omitted from the map (rather than sent empty)
// when the channel has no artwork.
func buildMetadata(trackID dbus.ObjectPath, station, track, artist, artURL string) map[string]dbus.Variant {
	// Sanitize strings to ensure valid UTF8 for D-Bus
	station = SanitizeUTF8(station)
	track = SanitizeUTF8(track)
	artist = SanitizeUTF8(artist)
	artURL = SanitizeUTF8(artURL)

	metadata := map[string]dbus.Variant{
		"mpris:trackid": dbus.MakeVariant(trackID),
		"xesam:title":   dbus.MakeVariant(track),
		"xesam:artist":  dbus.MakeVariant([]string{artist}),
		"xesam:album":   dbus.MakeVariant(station),
	}
	if artURL != "" {
		metadata["mpris:artUrl"] = dbus.MakeVariant(artURL)
	}
	return metadata
}

// SetVolume mirrors the player volume to the MPRIS Volume property.
func (m *MPRIS) SetVolume(v float64) {
	if m.props == nil {
		return
	}
	m.setProp("Volume", v)
}

// onVolumeChange forwards D-Bus writes to the Volume property (e.g. from a
// desktop volume slider) to the application.
func (m *MPRIS) onVolumeChange(c *prop.Change) *dbus.Error {
	if v, ok := c.Value.(float64); ok {
		m.send(MPRISVolumeMsg{Volume: v})
	}
	return nil
}

// SetStopped updates the playback status to stopped.
func (m *MPRIS) SetStopped() {
	if m.props == nil {
		return
	}
	m.setProp("PlaybackStatus", "Stopped")
	m.setProp("Metadata", map[string]dbus.Variant{})
}

// Close releases D-Bus resources.
func (m *MPRIS) Close() {
	m.closed.Store(true)
	if m.conn != nil {
		_, _ = m.conn.ReleaseName(busName)
		_ = m.conn.Close()
	}
}

// org.mpris.MediaPlayer2 methods

func (r *mprisRoot) Raise() *dbus.Error {
	return nil
}

func (r *mprisRoot) Quit() *dbus.Error {
	// CanQuit is advertised as true, so desktop shells show a Quit action;
	// route it to the server like the tray's Quit item.
	r.mpris.send(MPRISQuitMsg{})
	return nil
}

// org.mpris.MediaPlayer2.Player methods

func (p *mprisPlayer) Next() *dbus.Error {
	p.mpris.send(MPRISNextMsg{})
	return nil
}

func (p *mprisPlayer) Previous() *dbus.Error {
	p.mpris.send(MPRISPrevMsg{})
	return nil
}

func (p *mprisPlayer) Pause() *dbus.Error {
	p.mpris.send(MPRISStopMsg{})
	return nil
}

func (p *mprisPlayer) PlayPause() *dbus.Error {
	p.mpris.send(MPRISPlayPauseMsg{})
	return nil
}

func (p *mprisPlayer) Stop() *dbus.Error {
	p.mpris.send(MPRISStopMsg{})
	return nil
}

func (p *mprisPlayer) Play() *dbus.Error {
	p.mpris.send(MPRISPlayMsg{})
	return nil
}

// SeekOffset is the D-Bus Seek method (see playerMethodNames). It does
// nothing: live radio cannot seek, and with CanSeek false the spec asks for
// a no-op. It must still be exported (return *dbus.Error), or a client that
// seeks anyway gets UnknownMethod.
func (p *mprisPlayer) SeekOffset(_ int64) *dbus.Error {
	return nil
}

func (p *mprisPlayer) SetPosition(_ dbus.ObjectPath, _ int64) *dbus.Error {
	return nil
}

func (p *mprisPlayer) OpenUri(_ string) *dbus.Error {
	return nil
}
