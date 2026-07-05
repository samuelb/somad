package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"somatui/internal/app"
	"somatui/internal/audio"
	"somatui/internal/client"
	"somatui/internal/platform"
	"somatui/internal/protocol"
	"somatui/internal/server"
	"somatui/internal/state"
	"somatui/internal/ui"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Version information (set via ldflags during build)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func userAgent() string {
	return "SomaTUI/" + version
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runTUI()
		return
	}

	switch args[0] {
	case "--version", "-v", "version":
		fmt.Printf("somatui %s (commit: %s, built: %s)\n", version, commit, date)
	case "server":
		if len(args) > 1 && args[1] == "stop" {
			runServerStop()
			return
		}
		runServer(args[1:])
	case "play":
		runPlay(args[1:])
	case "next":
		runPlayRelative(1)
	case "prev", "previous":
		runPlayRelative(-1)
	case "pause":
		runPause()
	case "stop":
		runStop()
	case "status":
		runStatus()
	case "volume":
		runVolume(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "somatui: unknown command %q\n\n", args[0])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  somatui                        start the TUI (spawns the playback server if needed)
  somatui play [channel]         play a channel by ID or name, or resume the
                                 last played channel (spawns the server if needed)
  somatui next                   play the next channel (favorites first, wraps)
  somatui prev                   play the previous channel
  somatui pause                  toggle pause (reconnects the live stream on unpause)
  somatui stop                   stop playback
  somatui status                 show what is playing
  somatui volume <0-100>         set the playback volume
  somatui server [flags]         run the playback server in the foreground
  somatui server stop            shut down the playback server
  somatui --version              print version information
`)
}

// runServer runs the playback daemon: it owns audio, the channel catalog,
// persisted state, and MPRIS, and serves clients on the Unix socket.
func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	idleTimeout := fs.Duration("idle-timeout", server.DefaultIdleTimeout,
		"exit after this long with no clients and stopped playback (0 disables)")
	_ = fs.Parse(args)

	// Bind the socket before the (potentially slow) audio init: a bound
	// socket is the readiness signal spawning clients poll for, and taking
	// the lock early makes concurrent auto-spawns exit quickly. Connections
	// arriving before Run starts simply queue in the listen backlog.
	socketPath := protocol.SocketPath()
	ln, cleanup, err := server.Listen(socketPath)
	if errors.Is(err, server.ErrAlreadyRunning) {
		// A concurrent auto-spawn lost the race; the winner serves everyone.
		log.Print("somatui server already running, exiting")
		return
	}
	if err != nil {
		log.Fatalf("error starting server: %v", err)
	}
	log.Printf("somatui server %s listening on %s", version, socketPath)

	player, err := audio.NewPlayer(userAgent())
	if err != nil {
		cleanup()
		log.Fatalf("error initializing the audio player: %v", err)
	}

	appState, err := state.LoadState()
	if err != nil {
		cleanup()
		log.Fatalf("error loading state: %v", err)
	}

	mpris, err := platform.NewMPRIS()
	if err != nil {
		// MPRIS is optional, continue without it
		log.Printf("warning: MPRIS initialization failed: %v", err)
	}

	srv := server.New(server.Config{
		Version:     version,
		UserAgent:   userAgent(),
		Player:      player,
		State:       appState,
		MPRIS:       mpris,
		IdleTimeout: *idleTimeout,
	})

	// The server must survive its spawning terminal closing; SIGINT/SIGTERM
	// shut it down cleanly.
	signal.Ignore(syscall.SIGHUP)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Shutdown()
	}()

	err = srv.Run(ln)
	cleanup()
	if err != nil {
		log.Fatalf("server error: %v", err)
	}
	// Shutdown's player.Stop fades out asynchronously; give it a moment so
	// the audio doesn't cut off hard.
	time.Sleep(400 * time.Millisecond)
}

func runTUI() {
	socketPath := protocol.SocketPath()
	c, hr, err := client.EnsureServer(socketPath, version)
	if err != nil {
		fmt.Printf("Alas, there's been an error reaching the somatui server: %v\n", err)
		os.Exit(1)
	}

	// Create the main application model (need playing ID for delegate)
	m := &app.Model{
		Backend: c,
		Loading: true,
		About: app.AboutInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
	}
	if hr.ServerVersion != version {
		m.VersionSkew = hr.ServerVersion
	}

	// Initialize the Bubble Tea list component with styled delegate
	delegate := ui.NewStyledDelegate(&m.PlayingID, m.IsMatch, m.IsFavorite)
	l := list.New([]list.Item{}, delegate, 0, 0)
	l.SetShowTitle(false)        // We render our own header with column titles
	l.SetFilteringEnabled(false) // Disable filtering, we use search instead
	l.SetStatusBarItemName("channel", "channels")
	l.Styles.PaginationStyle = lipgloss.NewStyle().Foreground(ui.SubtleColor)
	l.Styles.HelpStyle = lipgloss.NewStyle().Foreground(ui.SubtleColor).Padding(0, 0, 0, 2)

	fullHelp, shortHelp := app.NewHelpKeys()
	l.AdditionalFullHelpKeys = func() []key.Binding {
		return fullHelp
	}
	l.AdditionalShortHelpKeys = func() []key.Binding {
		return shortHelp
	}
	m.List = l

	// Start the Bubble Tea program with window size handling
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Bridge server events into the Bubble Tea program, reconnecting (and
	// respawning the server) when the connection drops.
	go runBridge(p, c, socketPath)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v\n", err)
		os.Exit(1)
	}
}

// runBridge forwards server events to the program. When the connection is
// lost it re-establishes it (spawning a new server if needed) and hands the
// fresh client to the model.
func runBridge(p *tea.Program, c *client.Client, socketPath string) {
	for {
		for ev := range c.Events() {
			switch v := ev.(type) {
			case protocol.PlaybackState:
				p.Send(app.ServerStateMsg{State: v})
			case protocol.ChannelsPayload:
				p.Send(app.ServerChannelsMsg{Payload: v})
			}
		}

		p.Send(app.ServerLostMsg{})
		newClient, err := reconnect(socketPath)
		if err != nil {
			p.Send(app.ServerGoneMsg{Err: err})
			return
		}
		c = newClient
		p.Send(app.ServerReconnectedMsg{Backend: c})
	}
}

// reconnect tries a few times to get a fresh server connection.
func reconnect(socketPath string) (*client.Client, error) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var c *client.Client
		c, _, err = client.EnsureServer(socketPath, version)
		if err == nil {
			return c, nil
		}
		time.Sleep(time.Second)
	}
	return nil, fmt.Errorf("lost connection to the somatui server and could not restore it: %w", err)
}
