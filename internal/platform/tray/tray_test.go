//go:build darwin || linux

package tray

import (
	"testing"

	"somad/internal/platform"
)

func TestPlaybackLabel(t *testing.T) {
	tests := []struct {
		name    string
		station string
		track   string
		want    string
	}{
		{"both", "Groove Salad", "Fila Brazillia - Air", "Groove Salad — Fila Brazillia - Air"},
		{"station only", "Groove Salad", "", "Groove Salad"},
		{"track only", "", "Some Track", "Some Track"},
		{"neither", "", "", "Playing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := playbackLabel(tt.station, tt.track); got != tt.want {
				t.Errorf("playbackLabel(%q, %q) = %q, want %q", tt.station, tt.track, got, tt.want)
			}
		})
	}
}

// recordingSender captures messages routed from tray clicks.
type recordingSender struct{ msgs []any }

func (r *recordingSender) Send(msg any) { r.msgs = append(r.msgs, msg) }

func TestSendRoutesToSender(t *testing.T) {
	tr := New()
	rec := &recordingSender{}
	tr.SetSender(rec)

	tr.send(platform.MPRISPlayPauseMsg{})
	tr.send(platform.MPRISNextMsg{})

	if len(rec.msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(rec.msgs))
	}
	if _, ok := rec.msgs[0].(platform.MPRISPlayPauseMsg); !ok {
		t.Errorf("first message = %T, want MPRISPlayPauseMsg", rec.msgs[0])
	}
	if _, ok := rec.msgs[1].(platform.MPRISNextMsg); !ok {
		t.Errorf("second message = %T, want MPRISNextMsg", rec.msgs[1])
	}
}

func TestSendWithoutSenderIsNoop(t *testing.T) {
	tr := New()
	// No sender set: must not panic.
	tr.send(platform.MPRISStopMsg{})
}

func TestQuitInvokesCallback(t *testing.T) {
	tr := New()
	called := false
	tr.SetOnQuit(func() { called = true })
	tr.quit()
	if !called {
		t.Error("quit did not invoke the onQuit callback")
	}
}

func TestChannelItemTitle(t *testing.T) {
	tests := []struct {
		name    string
		ch      Channel
		playing bool
		want    string
	}{
		{"plain", Channel{Title: "Drone Zone"}, false, "Drone Zone"},
		{"favorite", Channel{Title: "Drone Zone", Favorite: true}, false, "★ Drone Zone"},
		{"playing", Channel{Title: "Drone Zone"}, true, "▸ Drone Zone"},
		{"favorite playing", Channel{Title: "Drone Zone", Favorite: true}, true, "▸ ★ Drone Zone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := channelItemTitle(tt.ch, tt.playing); got != tt.want {
				t.Errorf("channelItemTitle(%+v, %v) = %q, want %q", tt.ch, tt.playing, got, tt.want)
			}
		})
	}
}

func TestSetChannelsCopiesBeforeReady(t *testing.T) {
	tr := New()
	in := []Channel{{ID: "a", Title: "A"}, {ID: "b", Title: "B", Favorite: true}}
	tr.SetChannels(in)
	// Mutating the caller's slice must not affect the tray's copy.
	in[0].Title = "mutated"
	if len(tr.channels) != 2 || tr.channels[0].Title != "A" || tr.channels[1].ID != "b" {
		t.Errorf("SetChannels did not copy channels: %+v", tr.channels)
	}
}

func TestFavoriteLocked(t *testing.T) {
	tr := New()
	tr.SetChannels([]Channel{{ID: "a", Title: "A"}, {ID: "b", Title: "B", Favorite: true}})
	if tr.favoriteLocked("a") {
		t.Error("favoriteLocked(a) = true, want false")
	}
	if !tr.favoriteLocked("b") {
		t.Error("favoriteLocked(b) = false, want true")
	}
	if tr.favoriteLocked("missing") {
		t.Error("favoriteLocked(missing) = true, want false")
	}
}

// SetPlaying/SetStopped are safe to call before the menu is built (not ready);
// they must only update state and never touch systray.
func TestStateUpdatesBeforeReady(t *testing.T) {
	tr := New()
	tr.SetPlaying("groovesalad", "Groove Salad", "A Track")
	if !tr.playing || tr.playingID != "groovesalad" || tr.station != "Groove Salad" || tr.track != "A Track" {
		t.Errorf("SetPlaying did not record state: %+v", tr)
	}
	tr.SetStopped()
	if tr.playing || tr.playingID != "" || tr.station != "" || tr.track != "" {
		t.Errorf("SetStopped did not clear state: %+v", tr)
	}
}
