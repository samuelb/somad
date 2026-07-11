package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"somad/internal/app"
	"somad/internal/audio"
	"somad/internal/client"
	"somad/internal/config"
	"somad/internal/platform"
	"somad/internal/platform/tray"
	"somad/internal/protocol"
	"somad/internal/server"
	"somad/internal/state"
	"somad/internal/tlsutil"
	"somad/internal/ui"

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
	return "soma/" + version
}

func main() {
	args := os.Args[1:]

	// The word forms print to stdout and never touch the config file.
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v", "version":
			fmt.Printf("soma %s (commit: %s, built: %s)\n", version, commit, date)
			return
		case "--help", "-h", "help":
			printUsage(os.Stdout)
			return
		}
	}

	// Global flags precede the command. Parsing stops at the first non-flag
	// argument, so a command's own flags (e.g. soma daemon --listen) are
	// left alone for the command to parse itself.
	fs := flag.NewFlagSet("soma", flag.ExitOnError)
	fs.Usage = func() { printUsage(fs.Output()) }
	var cf connFlags
	fs.StringVar(&cf.server, "server", "", "connect to the soma daemon at this host:port instead of the local one")
	fs.BoolVar(&cf.tls, "tls", false, "use TLS for the --server connection")
	fs.StringVar(&cf.tlsCA, "tls-ca", "", "PEM certificate/CA file to trust (implies --tls)")
	fs.StringVar(&cf.tlsFingerprint, "tls-fingerprint", "", "pin the server certificate by SHA-256 fingerprint (implies --tls)")
	fs.StringVar(&cf.pskFile, "psk-file", "", "file holding the server's pre-shared key")
	shutdownOnExit := fs.Bool("shutdown-on-exit", false, "stop playback and shut down the server when the TUI exits")
	showVersion := fs.Bool("version", false, "print version information")
	_ = fs.Parse(args)
	if *showVersion {
		fmt.Printf("soma %s (commit: %s, built: %s)\n", version, commit, date)
		return
	}
	rest := fs.Args()

	// The daemon-start form dispatches before anything client-side happens;
	// only `soma daemon stop` is a client command and falls through.
	if len(rest) > 0 && rest[0] == "daemon" && (len(rest) < 2 || rest[1] != "stop") {
		// The global client flags don't apply to the daemon itself; refuse
		// rather than silently ignoring them.
		if fs.NFlag() > 0 {
			fail("daemon flags go after the subcommand: soma daemon [flags]")
		}
		runServer(rest[1:])
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fail("error loading config: %v", err)
	}
	endpoint, err = resolveEndpoint(cf, cfg)
	if err != nil {
		fail("%v", err)
	}

	if len(rest) == 0 {
		// The config file supplies the default only when the flag was not
		// given explicitly.
		so := *shutdownOnExit
		if !flagWasSet(fs, "shutdown-on-exit") && cfg.TUI.ShutdownOnExit != nil {
			so = *cfg.TUI.ShutdownOnExit
		}
		runTUI(so)
		return
	}

	switch rest[0] {
	case "daemon": // only `daemon stop` gets here (see above)
		if len(rest) != 2 {
			fail("usage: soma daemon stop")
		}
		runServerStop()
	case "play":
		runPlay(rest[1:])
	case "list":
		runList(rest[1:])
	case "favorite", "fav":
		runFavorite(rest[1:])
	case "next":
		runPlayRelative(1)
	case "prev", "previous":
		runPlayRelative(-1)
	case "pause":
		runPause()
	case "stop":
		runStop()
	case "status":
		runStatus(rest[1:])
	case "volume":
		runVolume(rest[1:])
	default:
		fmt.Fprintf(os.Stderr, "soma: unknown command %q\n\n", rest[0])
		printUsage(os.Stderr)
		os.Exit(2)
	}
}

// flagWasSet reports whether the flag was given explicitly on the command
// line (as opposed to resting at its default).
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// printFlagDefaults mirrors flag.PrintDefaults, but shows the options with
// two dashes so a FlagSet's help matches the rest of soma's help output.
func printFlagDefaults(fs *flag.FlagSet) {
	fs.VisitAll(func(f *flag.Flag) {
		var b strings.Builder
		fmt.Fprintf(&b, "  --%s", f.Name)
		valueName, usage := flag.UnquoteUsage(f)
		if valueName != "" {
			fmt.Fprintf(&b, " %s", valueName)
		}
		b.WriteString("\n    \t")
		b.WriteString(strings.ReplaceAll(usage, "\n", "\n    \t"))
		switch f.DefValue {
		case "", "false", "0", "0s": // zero values are not worth printing
		default:
			fmt.Fprintf(&b, " (default %v)", f.DefValue)
		}
		_, _ = fmt.Fprintln(fs.Output(), b.String())
	})
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage:
  soma                        start the TUI (spawns the playback server if needed)
                                 (--shutdown-on-exit stops playback and server on quit)
  soma play [channel]         play a channel by ID or name, or resume the
                                 last played channel (spawns the server if needed)
  soma list [--json]          list all channels (favorites first, marked *)
  soma favorite [--json] <channel>
                                 toggle a channel's favorite flag
  soma next                   play the next channel (favorites first, wraps)
  soma prev                   play the previous channel
  soma pause                  toggle pause (reconnects the live stream on unpause)
  soma stop                   stop playback
  soma status [--json]        show what is playing
  soma volume [<0-100>|+n|-n] show, set, or adjust the playback volume
  soma daemon [flags]         run the playback server in the foreground
                                 (--no-tray hides the tray / menu-bar icon;
                                  --listen <host:port> also serves frontends
                                  over TCP, --tls encrypts it, --psk-file
                                  requires a pre-shared key, --show-cert
                                  prints the TLS certificate fingerprint)
  soma daemon stop            shut down the playback server
  soma --version              print version information
  soma --help                 show this help

Connection flags (given before the command) reach a soma daemon on another
machine instead of the local one:
  --server <host:port>        connect over TCP (also via $SOMAD_SERVER)
  --tls                       use TLS (implied by the two flags below)
  --tls-ca <file>             trust this PEM certificate/CA
  --tls-fingerprint <fp>      pin the server certificate ("sha256:...", as
                                 printed by soma daemon --show-cert)
  --psk-file <file>           read the server's pre-shared key from a file
`)
	if path, err := config.Path(); err == nil {
		_, _ = fmt.Fprintf(w, `
Server and connection flags can also be set in %s
(explicit flags take precedence), for example:
  server:
    idle_timeout: 5m   # exit after this long idle (default "0": never)
    tray: false        # hide the tray / menu-bar icon
    listen: ":5454"    # also serve frontends over TCP
    tls: true          # ...encrypted (auto-generated certificate)
    psk: "secret"      # ...and authenticated
  client:
    server: "myserver:5454"
    tls_fingerprint: "sha256:..."
    psk: "secret"
  tui:
    shutdown_on_exit: true
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
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: soma daemon [flags]")
		_, _ = fmt.Fprintln(fs.Output(), "Flags:")
		printFlagDefaults(fs)
	}
	idleTimeout := fs.Duration("idle-timeout", defaultIdleTimeout,
		"exit after this long with no clients and stopped playback (0 disables)")
	noTray := fs.Bool("no-tray", defaultNoTray,
		"do not show the system tray / menu-bar icon while the server runs")
	listen := fs.String("listen", str(cfg.Server.Listen),
		"also listen for frontends on this TCP host:port (empty: Unix socket only)")
	tlsOn := fs.Bool("tls", cfg.Server.TLS != nil && *cfg.Server.TLS,
		"serve the TCP listener over TLS (a certificate is generated when none is configured)")
	tlsCert := fs.String("tls-cert", str(cfg.Server.TLSCert),
		"PEM certificate for the TCP listener (implies --tls; requires --tls-key)")
	tlsKey := fs.String("tls-key", str(cfg.Server.TLSKey),
		"PEM private key belonging to --tls-cert")
	pskFile := fs.String("psk-file", str(cfg.Server.PSKFile),
		"file holding the pre-shared key TCP clients must authenticate with")
	showCert := fs.Bool("show-cert", false,
		"print the TLS certificate path and fingerprint, then exit")
	_ = fs.Parse(args)

	certPath, keyPath := *tlsCert, *tlsKey
	if (certPath == "") != (keyPath == "") {
		log.Fatal("--tls-cert and --tls-key (or tls_cert/tls_key in the config) must be set together")
	}
	tlsEnabled := *tlsOn || certPath != ""
	// The certificate is resolved (and generated) even for --show-cert with
	// TLS not yet enabled: the user is pairing a client right now.
	if tlsEnabled || *showCert {
		var err error
		if certPath, keyPath, err = ensureCertPair(certPath, keyPath, *listen); err != nil {
			log.Fatalf("error preparing the TLS certificate: %v", err)
		}
	}
	if *showCert {
		_, fingerprint, err := tlsutil.ServerTLSConfig(certPath, keyPath)
		if err != nil {
			log.Fatalf("error loading the TLS certificate: %v", err)
		}
		fmt.Printf("certificate: %s\nfingerprint: %s\n", certPath, fingerprint)
		return
	}

	psk := str(cfg.Server.PSK)
	if *pskFile != "" {
		var err error
		if psk, err = readPSKFile(*pskFile); err != nil {
			log.Fatalf("error reading the PSK file: %v", err)
		}
	}

	// Bind the socket before the (potentially slow) audio init: a bound
	// socket is the readiness signal spawning clients poll for, and taking
	// the lock early makes concurrent auto-spawns exit quickly. Connections
	// arriving before Run starts simply queue in the listen backlog.
	socketPath := protocol.SocketPath()
	ln, cleanup, err := server.Listen(socketPath)
	if errors.Is(err, server.ErrAlreadyRunning) {
		// A concurrent auto-spawn lost the race; the winner serves everyone.
		log.Print("soma daemon already running, exiting")
		return
	}
	if err != nil {
		log.Fatalf("error starting server: %v", err)
	}
	log.Printf("soma daemon %s listening on %s", version, socketPath)

	listeners := []net.Listener{ln}
	if *listen != "" {
		tcpLn, err := listenTCP(*listen, tlsEnabled, certPath, keyPath, psk)
		if err != nil {
			cleanup()
			log.Fatalf("error starting the TCP listener: %v", err)
		}
		listeners = append(listeners, tcpLn)
	}

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
		PSK:         psk,
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
			runErrCh <- srv.Run(listeners...)
			srv.Shutdown() // idempotent; unblocks the tray on any exit path
		}()
		tr.Run(nil)
		runErr = <-runErrCh
	} else {
		runErr = srv.Run(listeners...)
	}
	cleanup()
	if runErr != nil {
		log.Fatalf("server error: %v", runErr)
	}
	// Shutdown's player.Stop fades out asynchronously; give it a moment so
	// the audio doesn't cut off hard.
	time.Sleep(400 * time.Millisecond)
}

// ensureCertPair resolves the server certificate pair, generating a
// self-signed one in the state directory when none is configured. The listen
// address's host (when it is a specific name or IP) goes into the
// certificate's SANs.
func ensureCertPair(certPath, keyPath, listenAddr string) (string, string, error) {
	if certPath != "" {
		return certPath, keyPath, nil
	}
	dir, err := state.Dir()
	if err != nil {
		return "", "", err
	}
	certPath = filepath.Join(dir, "tls-cert.pem")
	keyPath = filepath.Join(dir, "tls-key.pem")

	var hosts []string
	if host, _, err := net.SplitHostPort(listenAddr); err == nil && host != "" {
		if ip := net.ParseIP(host); ip == nil || !ip.IsUnspecified() {
			hosts = append(hosts, host)
		}
	}
	created, err := tlsutil.EnsureServerCert(certPath, keyPath, hosts)
	if err != nil {
		return "", "", err
	}
	if created {
		log.Printf("generated a self-signed TLS certificate at %s", certPath)
	}
	return certPath, keyPath, nil
}

// listenTCP binds the remote-frontend listener, wrapping it in TLS when
// enabled, and logs what protections it runs with — including prominent
// warnings for the combinations that leave it open.
func listenTCP(addr string, useTLS bool, certPath, keyPath, psk string) (net.Listener, error) {
	tcpLn, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, err
	}
	if useTLS {
		tlsCfg, fingerprint, err := tlsutil.ServerTLSConfig(certPath, keyPath)
		if err != nil {
			_ = tcpLn.Close()
			return nil, err
		}
		tcpLn = tls.NewListener(tcpLn, tlsCfg)
		log.Printf("listening on tcp://%s with TLS, certificate fingerprint %s", tcpLn.Addr(), fingerprint)
	} else {
		log.Printf("WARNING: the TCP listener on %s is unencrypted (no TLS); anyone on the network can observe it", tcpLn.Addr())
	}
	if psk == "" {
		log.Printf("WARNING: the TCP listener on %s requires no authentication (no PSK); anyone who can reach it controls playback", tcpLn.Addr())
	}
	return tcpLn, nil
}

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

	// Start the Bubble Tea program with window size handling
	p := tea.NewProgram(m, tea.WithAltScreen())

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
	for attempt := 0; attempt < 3; attempt++ {
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
