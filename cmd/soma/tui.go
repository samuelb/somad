package main

import (
	"fmt"
	"os"
	"sync"
	"time"

	"somad/internal/app"
	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/ui"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		// change or stop restarts it onto our version.
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

	// Initialize the Bubble Tea list component with styled delegate
	delegate := ui.NewStyledDelegate(&m.PlayingID, m.IsMatch, m.IsFavorite)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false)        // We render our own header with column titles
	l.SetFilteringEnabled(false) // Disable filtering, we use search instead
	l.SetStatusBarItemName("channel", "channels")
	// The bubbles default binds "h" to previous page; "h" is used for the
	// history overlay instead (see the keymap in internal/app/update.go), so
	// drop it here rather than silently shadowing it with no help text to
	// match.
	l.KeyMap.PrevPage = key.NewBinding(
		key.WithKeys("left", "pgup", "b", "u"),
		key.WithHelp("←/pgup", "prev page"),
	)
	l.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(ui.SubtleColor)
	l.Styles.HelpStyle = lipgloss.NewStyle().Foreground(ui.SubtleColor).Padding(0, 0, 0, 2)

	fullHelp, shortHelp := app.NewHelpKeys(shutdownOnExit)
	l.AdditionalFullHelpKeys = func() []key.Binding {
		return fullHelp
	}
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return shortHelp
	}
	m.List = l

	// Start the Bubble Tea program with window size handling. Mouse cell
	// motion reporting lets the mouse wheel scroll the channel list (see
	// internal/app/update.go's tea.MouseMsg handling: the vendored
	// bubbles/list does not process mouse events on its own).
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	// Bridge server events into the Bubble Tea program, reconnecting (and
	// respawning the server) when the connection drops.
	bridgeExited := make(chan struct{})
	go func() {
		defer close(bridgeExited)
		runBridge(p, c, bridgeDone, shutdownOnExit)
	}()

	if _, err := p.Run(); err != nil {
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
