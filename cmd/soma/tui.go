package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"somad/internal/app"
	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/state"
	"somad/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func runTUI(shutdownOnExit bool) {
	c, hr, err := client.EnsureServer(endpoint, version)
	if err != nil {
		fmt.Printf("Alas, there's been an error reaching the soma daemon: %v\n", err)
		os.Exit(1)
	}

	// Create the main application model (need playing ID for delegate)
	m := &app.Model{
		Backend: c,
		// A skewed server keeps playing while the user browses; the next channel
		// change, pause or stop restarts it onto our version.
		ServerVersion:  hr.ServerVersion,
		Loading:        true,
		ShutdownOnExit: shutdownOnExit,
		About: app.AboutInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
	}

	bridgeDone := make(chan struct{})
	var bridgeDoneOnce sync.Once
	m.OnExit = func() {
		bridgeDoneOnce.Do(func() {
			close(bridgeDone)
		})
	}

	m.List = m.NewList()

	// The visualizer style picked last time comes back; see saveVisualizer.
	prefs, prefsErr := state.LoadTUIPrefs()
	m.Visualizer, _ = ui.ParseVisualizerMode(prefs.Visualizer)

	// Start the Bubble Tea program with window size handling. Mouse cell
	// motion reporting lets the mouse wheel scroll the channel list (see
	// internal/app/update.go's tea.MouseMsg handling: the vendored
	// bubbles/list does not process mouse events on its own).
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	saver := newVisualizerSaver(saveVisualizer, func(err error) {
		p.Send(app.RequestErrorMsg{Op: "saving the visualizer style", Err: err})
	})
	m.OnVisualizer = saver.Offer
	if prefsErr != nil {
		go p.Send(app.RequestErrorMsg{Op: "loading the visualizer style", Err: prefsErr})
	}

	// Bridge server events into the Bubble Tea program, reconnecting (and
	// respawning the server) when the connection drops.
	bridgeExited := make(chan struct{})
	go func() {
		defer close(bridgeExited)
		runBridge(p, c, bridgeDone, shutdownOnExit)
	}()

	_, err = p.Run()
	saver.Close() // a style picked just before quitting is still written
	if err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		os.Exit(1)
	}
	m.OnExit()
	if shutdownOnExit {
		// The bridge may be mid-reconnect, about to spawn a replacement
		// server; wait for it so that server is shut down too, not orphaned.
		<-bridgeExited
	}
}

// saveVisualizer remembers the visualizer style in the TUI's preferences
// (internal/state's tui.json), keeping any other preference there.
func saveVisualizer(mode ui.VisualizerMode) error {
	prefs, err := state.LoadTUIPrefs()
	if err != nil {
		return err
	}
	prefs.Visualizer = mode.String()
	return state.SaveTUIPrefs(prefs)
}

// runBridge forwards server events to the program. When the connection is
// lost it re-establishes it (spawning a new local server if needed) and hands
// the fresh client, and its version, to the model.
func runBridge(p *tea.Program, c *client.Client, done <-chan struct{}, shutdownOnExit bool) {
	for {
	events:
		for {
			select {
			case <-done:
				return
			case ev, ok := <-c.Events():
				if !ok {
					break events
				}
				switch v := ev.(type) {
				case protocol.PlaybackState:
					p.Send(app.ServerStateMsg{State: v})
				case protocol.ChannelsPayload:
					p.Send(app.ServerChannelsMsg{Payload: v})
				}
			case levels := <-c.Spectrum():
				p.Send(app.SpectrumMsg{Levels: levels})
			}
		}

		p.Send(app.ServerLostMsg{})
		select {
		case <-done:
			return
		default:
		}
		newClient, serverVersion, err := reconnect()
		if err != nil {
			p.Send(app.ServerGoneMsg{Err: err})
			return
		}
		select {
		case <-done:
			// The TUI quit while reconnect was (possibly) spawning a fresh
			// server; honor shutdown-on-exit instead of orphaning it.
			if shutdownOnExit {
				_ = newClient.Shutdown()
			}
			_ = newClient.Close()
			return
		default:
		}
		_ = c.Close()
		c = newClient
		p.Send(app.ServerReconnectedMsg{Backend: c, ServerVersion: serverVersion})
	}
}

// reconnect tries a few times to get a fresh server connection, returning the
// reconnected server's version alongside the client.
func reconnect() (*client.Client, string, error) {
	var err error
	for range 3 {
		var c *client.Client
		var hr protocol.HelloResult
		c, hr, err = client.EnsureServer(endpoint, version)
		if err == nil {
			return c, hr.ServerVersion, nil
		}
		time.Sleep(time.Second)
	}
	return nil, "", fmt.Errorf("lost connection to the soma daemon and could not restore it: %w", err)
}
