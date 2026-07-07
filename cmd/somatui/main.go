package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"somatui/internal/app"
	"somatui/internal/audio"
	"somatui/internal/client"
	"somatui/internal/config"
	"somatui/internal/platform"
	"somatui/internal/platform/tray"
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
	case "--help", "-h", "help":
		printUsage(os.Stdout)
	case "server":
		if len(args) > 1 && args[1] == "stop" {
			runServerStop()
			return
		}
		runServer(args[1:])
	case "play":
		runPlay(args[1:])
	case "list":
		runList(args[1:])
	case "favorite", "fav":
		runFavorite(args[1:])
	case "next":
		runPlayRelative(1)
	case "prev", "previous":
		runPlayRelative(-1)
	case "pause":
		runPause()
	case "stop":
		runStop()
	case "status":
		runStatus(args[1:])
	case "volume":
		runVolume(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "somatui: unknown command %q\n\n", args[0])
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  somatui                        start the TUI (spawns the playback server if needed)
  somatui play [channel]         play a channel by ID or name, or resume the
                                 last played channel (spawns the server if needed)
  somatui list [--json]          list all channels (favorites first, marked *)
  somatui favorite [--json] <channel>
                                 toggle a channel's favorite flag
  somatui next                   play the next channel (favorites first, wraps)
  somatui prev                   play the previous channel
  somatui pause                  toggle pause (reconnects the live stream on unpause)
  somatui stop                   stop playback
  somatui status [--json]        show what is playing
  somatui volume [<0-100>|+n|-n] show, set, or adjust the playback volume
  somatui server [flags]         run the playback server in the foreground
                                 (--no-tray hides the tray / menu-bar icon)
  somatui server stop            shut down the playback server
  somatui --version              print version information
  somatui --help                 show this help
`)
	if path, err := config.Path(); err == nil {
		_, _ = fmt.Fprintf(w, `
Server flags can also be set in %s
(explicit flags take precedence), for example:
  server:
    idle_timeout: 5m   # exit after this long idle (0 disables)
    tray: false        # hide the tray / menu-bar icon
`, path)
	}
}

// runServer runs the playback daemon: it owns audio, the channel catalog,
// persisted state, and MPRIS, and serves clients on the Unix socket.
func runServer(args []string) {
	// On first start, materialize a commented-out template so the settings
	// are discoverable; failing to (e.g. a read-only home) is no reason not
	// to run.
	if path, created, err := config.EnsureTemplate(server.DefaultIdleTimeout); err != nil {
		log.Printf("warning: could not write the default config template: %v", err)
	} else if created {
		log.Printf("wrote a default config template to %s", path)
	}

	// The config file supplies the flag defaults, so explicit flags override
	// it, and an auto-spawned server (which gets no flags) still honors it.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}
	defaultIdleTimeout := server.DefaultIdleTimeout
	if cfg.Server.IdleTimeout != nil {
		defaultIdleTimeout = time.Duration(*cfg.Server.IdleTimeout)
	}
	defaultNoTray := cfg.Server.Tray != nil && !*cfg.Server.Tray

	fs := flag.NewFlagSet("server", flag.ExitOnError)
	idleTimeout := fs.Duration("idle-timeout", defaultIdleTimeout,
		"exit after this long with no clients and stopped playback (0 disables)")
	noTray := fs.Bool("no-tray", defaultNoTray,
		"do not show the system tray / menu-bar icon while the server runs")
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

	// The tray icon lives in the server process, so it appears whenever the
	// server is running. It is skipped when disabled, unsupported, or when no
	// GUI is present (a headless host), so the server still runs anywhere.
	var tr *tray.Tray
	if !*noTray && tray.Available() {
		tr = tray.New()
	}

	srv := server.New(server.Config{
		Version:     version,
		UserAgent:   userAgent(),
		Player:      player,
		State:       appState,
		MPRIS:       mpris,
		Tray:        tr,
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

	// The tray owns the process's native GUI run loop and must run on the main
	// goroutine, so serve connections on a goroutine and block on the tray.
	// srv.Shutdown (from a signal, the idle timer, or the tray's Quit item)
	// stops the tray, which unblocks Run. Without a tray, serve on the main
	// goroutine as before.
	var runErr error
	if tr != nil {
		runErrCh := make(chan error, 1)
		go func() {
			runErrCh <- srv.Run(ln)
			srv.Shutdown() // idempotent; unblocks the tray on any exit path
		}()
		tr.Run(nil)
		runErr = <-runErrCh
	} else {
		runErr = srv.Run(ln)
	}
	cleanup()
	if runErr != nil {
		log.Fatalf("server error: %v", runErr)
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
		// A skewed server keeps playing while the user browses; the next channel
		// change or stop restarts it onto our version.
		ServerVersion: hr.ServerVersion,
		Loading:       true,
		About: app.AboutInfo{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
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
// fresh client, and its version, to the model.
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
		newClient, serverVersion, err := reconnect(socketPath)
		if err != nil {
			p.Send(app.ServerGoneMsg{Err: err})
			return
		}
		c = newClient
		p.Send(app.ServerReconnectedMsg{Backend: c, ServerVersion: serverVersion})
	}
}

// reconnect tries a few times to get a fresh server connection, returning the
// reconnected server's version alongside the client.
func reconnect(socketPath string) (*client.Client, string, error) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var c *client.Client
		var hr protocol.HelloResult
		c, hr, err = client.EnsureServer(socketPath, version)
		if err == nil {
			return c, hr.ServerVersion, nil
		}
		time.Sleep(time.Second)
	}
	return nil, "", fmt.Errorf("lost connection to the somatui server and could not restore it: %w", err)
}
