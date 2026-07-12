//go:build linux

package platform

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

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

func TestSanitizeUTF8_ValidString(t *testing.T) {
	input := "Hello, World!"
	assert.Equal(t, input, SanitizeUTF8(input))
}

func TestSanitizeUTF8_ValidUnicode(t *testing.T) {
	input := "Café del Mar — Música Ambiental 日本語"
	assert.Equal(t, input, SanitizeUTF8(input))
}

func TestSanitizeUTF8_EmptyString(t *testing.T) {
	assert.Equal(t, "", SanitizeUTF8(""))
}

func TestSanitizeUTF8_InvalidBytes(t *testing.T) {
	// \xff is not valid UTF-8
	input := "Hello\xff World"
	result := SanitizeUTF8(input)
	assert.Equal(t, "Hello World", result)
}

func TestSanitizeUTF8_AllInvalid(t *testing.T) {
	input := "\xff\xfe\xfd"
	result := SanitizeUTF8(input)
	assert.Equal(t, "", result)
}

func TestSanitizeUTF8_MixedValidInvalid(t *testing.T) {
	input := "A\xffB\xfeC"
	result := SanitizeUTF8(input)
	assert.Equal(t, "ABC", result)
}
