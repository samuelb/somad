//go:build linux

package platform

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	meta := buildMetadata("Station", "Track", "Artist", "https://example.com/art.png")

	v, ok := meta["mpris:artUrl"]
	require.True(t, ok, "mpris:artUrl must be present when the channel has artwork")
	assert.Equal(t, "https://example.com/art.png", v.Value())
	assert.Equal(t, "Track", meta["xesam:title"].Value())
	assert.Equal(t, []string{"Artist"}, meta["xesam:artist"].Value())
	assert.Equal(t, "Station", meta["xesam:album"].Value())
}

func TestBuildMetadata_OmitsArtUrlWhenEmpty(t *testing.T) {
	meta := buildMetadata("Station", "Track", "Artist", "")

	_, ok := meta["mpris:artUrl"]
	assert.False(t, ok, "mpris:artUrl must be omitted, not sent empty, when the channel has no artwork")
}

func TestBuildMetadata_SanitizesArtUrl(t *testing.T) {
	meta := buildMetadata("Station", "Track", "Artist", "https://example.com/art\xff.png")

	assert.Equal(t, "https://example.com/art.png", meta["mpris:artUrl"].Value())
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
