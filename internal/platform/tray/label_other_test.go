//go:build darwin

package tray

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMenuLabel_IsTheTextItself(t *testing.T) {
	for _, text := range []string{"sub_mariner", "__init__", "Groove Salad", ""} {
		assert.Equal(t, text, menuLabel(text))
	}
}

// A macOS menu item's title is plain text: names reach it as written.
func TestTray_NamesWithUnderscoresReachTheMenuAsWritten(t *testing.T) {
	tr, menu := New(), &fakeMenu{}
	menu.attach(tr)
	menu.take()

	tr.SetChannels([]Channel{{ID: "a", Title: "Under_ground 80s"}})
	tr.SetPlaying("a", "Under_ground 80s", "The_Artist - sub_mariner")

	assert.Equal(t, []string{
		"channel 0: title Under_ground 80s",
		"title: title ♪ Under_ground 80s — The_Artist - sub_mariner",
		"play/stop: title Stop",
		"tooltip: Soma — Under_ground 80s — The_Artist - sub_mariner",
		"favorite: enable",
		"channel 0: title ▸ Under_ground 80s",
	}, menu.take())
}
