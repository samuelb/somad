//go:build linux

package platform

import (
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// recordingSender collects messages sent through the MPRIS command path.
type recordingSender struct {
	mu   sync.Mutex
	msgs []any
}

func (r *recordingSender) Send(msg any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
}

func (r *recordingSender) messages() []any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any(nil), r.msgs...)
}

func TestMPRIS_PlayerMethodsRouteToSender(t *testing.T) {
	m := &MPRIS{}
	s := &recordingSender{}
	m.SetSender(s)
	p := &mprisPlayer{mpris: m}

	assert.Nil(t, p.Next())
	assert.Nil(t, p.Previous())
	assert.Nil(t, p.Pause())
	assert.Nil(t, p.PlayPause())
	assert.Nil(t, p.Stop())
	assert.Nil(t, p.Play())
	assert.Nil(t, m.onVolumeChange(&prop.Change{Value: 0.5}))

	assert.Equal(t, []any{
		MPRISNextMsg{},
		MPRISPrevMsg{},
		MPRISStopMsg{},
		MPRISPlayPauseMsg{},
		MPRISStopMsg{},
		MPRISPlayMsg{},
		MPRISVolumeMsg{Volume: 0.5},
	}, s.messages())
}

func TestMPRIS_MethodsSafeWithoutSender(t *testing.T) {
	m := &MPRIS{}
	p := &mprisPlayer{mpris: m}
	assert.Nil(t, p.Play())
	assert.Nil(t, m.onVolumeChange(&prop.Change{Value: 0.5}))
}

// TestMPRIS_SetPlayingSafeWithoutProps guards the nil-props early return:
// m.props is only populated once a real D-Bus session bus connection exports
// properties, which tests do not have, so SetPlaying must not panic on a
// bare MPRIS.
func TestMPRIS_SetPlayingSafeWithoutProps(t *testing.T) {
	m := &MPRIS{}
	assert.NotPanics(t, func() { m.SetPlaying("Station", "Track", "Artist", "https://example.com/art.png") })
}

func TestMPRIS_SettersRecoverFromFailedPropertyUpdate(t *testing.T) {
	// A zero prop.Properties has no property table, so every SetMust fails
	// the way it does on a closed bus: with a panic that must not escape.
	m := &MPRIS{props: &prop.Properties{}}
	assert.NotPanics(t, func() {
		m.SetPlaying("Station", "Track", "Artist", "")
		m.SetVolume(0.5)
		m.SetStopped()
	})
}

func TestMPRIS_SettersAreNoopsAfterClose(t *testing.T) {
	m := &MPRIS{props: &prop.Properties{}}
	m.Close()
	assert.True(t, m.closed.Load())
	assert.NotPanics(t, func() {
		m.SetPlaying("Station", "Track", "Artist", "")
		m.SetVolume(0.5)
		m.SetStopped()
	})
}

func TestBuildMetadata_IncludesArtUrlWhenPresent(t *testing.T) {
	meta := buildMetadata(trackIDPrefix+"1", "Station", "Track", "Artist", "https://example.com/art.png")

	v, ok := meta["mpris:artUrl"]
	require.True(t, ok, "mpris:artUrl must be present when the channel has artwork")
	assert.Equal(t, "https://example.com/art.png", v.Value())
	assert.Equal(t, "Track", meta["xesam:title"].Value())
	assert.Equal(t, []string{"Artist"}, meta["xesam:artist"].Value())
	assert.Equal(t, "Station", meta["xesam:album"].Value())
}

func TestBuildMetadata_OmitsArtUrlWhenEmpty(t *testing.T) {
	meta := buildMetadata(trackIDPrefix+"1", "Station", "Track", "Artist", "")

	_, ok := meta["mpris:artUrl"]
	assert.False(t, ok, "mpris:artUrl must be omitted, not sent empty, when the channel has no artwork")
}

func TestBuildMetadata_SanitizesArtUrl(t *testing.T) {
	meta := buildMetadata(trackIDPrefix+"1", "Station", "Track", "Artist", "https://example.com/art\xff.png")

	assert.Equal(t, "https://example.com/art.png", meta["mpris:artUrl"].Value())
}

func TestBuildMetadata_CarriesTrackID(t *testing.T) {
	meta := buildMetadata(trackIDPrefix+"7", "Station", "Track", "Artist", "")

	assert.Equal(t, dbus.ObjectPath(trackIDPrefix+"7"), meta["mpris:trackid"].Value())
}

func TestMPRIS_TrackIDNamesEachTrack(t *testing.T) {
	m := &MPRIS{}

	first := m.trackID("Station", "Track", "Artist")
	assert.True(t, first.IsValid(), "mpris:trackid must be a valid object path")
	assert.False(t, strings.HasPrefix(string(first), "/org/mpris/"),
		"the spec reserves /org/mpris; track IDs must live elsewhere")
	assert.Equal(t, first, m.trackID("Station", "Track", "Artist"),
		"metadata re-sent for the same track keeps its ID")

	second := m.trackID("Station", "Next Track", "Artist")
	assert.NotEqual(t, first, second, "a new track gets a new ID")
	assert.NotEqual(t, second, m.trackID("Other Station", "Next Track", "Artist"),
		"so does the same title on another channel")
}

func TestMPRIS_AdvertisesNoURISupport(t *testing.T) {
	// OpenUri is a no-op, so advertising schemes or MIME types would
	// invite clients to hand over URIs that are silently dropped.
	root := (&MPRIS{}).propsSpec()[mprisInterface]

	for _, name := range []string{"SupportedUriSchemes", "SupportedMimeTypes"} {
		require.IsType(t, []string{}, root[name].Value, "%s must stay a string array (as)", name)
		assert.Empty(t, root[name].Value, name)
	}
}

// exportedMethods mirrors how godbus's Export/ExportWithMap build their
// method table: exported Go methods whose last result is *dbus.Error, under
// their mapped D-Bus name. Anything else is silently skipped.
func exportedMethods(v any, mapping map[string]string) map[string]reflect.Type {
	dbusErr := reflect.TypeOf((*dbus.Error)(nil))
	methods := map[string]reflect.Type{}
	val := reflect.ValueOf(v)
	for i := range val.NumMethod() {
		ft := val.Method(i).Type()
		if ft.NumOut() == 0 || ft.Out(ft.NumOut()-1) != dbusErr {
			continue
		}
		name := val.Type().Method(i).Name
		if mapped, ok := mapping[name]; ok {
			name = mapped
		}
		methods[name] = ft
	}
	return methods
}

// TestMPRIS_IntrospectedMethodsAreExported checks that every method the
// introspection data advertises is one godbus actually exports, with the
// advertised arguments; a client calling a skipped one gets UnknownMethod.
func TestMPRIS_IntrospectedMethodsAreExported(t *testing.T) {
	exported := map[string]map[string]reflect.Type{
		mprisInterface:  exportedMethods(&mprisRoot{}, nil),
		playerInterface: exportedMethods(&mprisPlayer{}, playerMethodNames),
	}

	for _, iface := range introspectNode().Interfaces {
		methods, ok := exported[iface.Name]
		if !ok {
			continue // the standard Introspectable and Properties interfaces
		}
		for _, method := range iface.Methods {
			t.Run(iface.Name+"."+method.Name, func(t *testing.T) {
				ft, ok := methods[method.Name]
				require.True(t, ok, "advertised but not exported")

				// Compare D-Bus signatures: the argument types concatenated.
				var wantIn, wantOut, gotIn, gotOut string
				for _, a := range method.Args {
					if a.Direction == "out" {
						wantOut += a.Type
					} else {
						wantIn += a.Type
					}
				}
				for i := range ft.NumIn() {
					gotIn += dbus.SignatureOfType(ft.In(i)).String()
				}
				for i := range ft.NumOut() - 1 {
					gotOut += dbus.SignatureOfType(ft.Out(i)).String()
				}
				assert.Equal(t, wantIn, gotIn, "in arguments")
				assert.Equal(t, wantOut, gotOut, "out arguments")
			})
		}
	}
}

func TestMPRIS_QuitRoutesToSender(t *testing.T) {
	m := &MPRIS{}
	s := &recordingSender{}
	m.SetSender(s)
	r := &mprisRoot{mpris: m}

	// CanQuit is advertised, so Quit must do something rather than leave
	// desktop shells with a dead menu item.
	assert.Nil(t, r.Quit())
	assert.Equal(t, []any{MPRISQuitMsg{}}, s.messages())
}

// TestMPRIS_SetSenderConcurrentWithHandlers fails under -race if sender is
// accessed without synchronization: D-Bus handlers run on godbus goroutines
// while SetSender is called after the bus objects are exported.
func TestMPRIS_SetSenderConcurrentWithHandlers(t *testing.T) {
	m := &MPRIS{}
	p := &mprisPlayer{mpris: m}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			m.SetSender(&recordingSender{})
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			_ = p.Next()
		}
	}()
	wg.Wait()
}
