//go:build linux

package tray

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMenuLabel_DoublesUnderscores(t *testing.T) {
	for text, want := range map[string]string{
		"sub_mariner":     "sub__mariner",
		"__init__":        "____init____",
		"_":               "__",
		"Groove Salad":    "Groove Salad",
		"♪ Drone Zone — ": "♪ Drone Zone — ",
		"":                "",
	} {
		assert.Equal(t, want, menuLabel(text), "%q", text)
	}
}

// A dbusmenu label takes an underscore for the mark of an access key and
// does not show it: the labels the tray makes of names from the network
// must carry two for each.
func TestTray_NamesWithUnderscoresReachTheMenuDoubled(t *testing.T) {
	tr, menu := New(), &fakeMenu{}
	menu.attach(tr)
	menu.take()

	tr.SetChannels([]Channel{{ID: "a", Title: "Under_ground 80s"}, {ID: "b", Title: "Plain", Favorite: true}})
	tr.SetPlaying("a", "Under_ground 80s", "The_Artist - sub_mariner")

	assert.Equal(t, []string{
		"channel 0: title Under__ground 80s",
		"channel 1: title ★ Plain",
		"title: title ♪ Under__ground 80s — The__Artist - sub__mariner",
		"play/stop: title Stop",
		"tooltip: Soma — Under_ground 80s — The_Artist - sub_mariner",
		"favorite: enable",
		"channel 0: title ▸ Under__ground 80s",
	}, menu.take(), "the tooltip is no menu label and stays as written")

	// The same names again are the same labels: nothing to update.
	tr.SetPlaying("a", "Under_ground 80s", "The_Artist - sub_mariner")
	assert.Empty(t, menu.take())
}
