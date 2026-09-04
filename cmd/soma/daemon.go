package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"somad/internal/atomicfile"
	"somad/internal/audio"
	"somad/internal/config"
	"somad/internal/lastfm"
	"somad/internal/platform"
	"somad/internal/platform/tray"
	"somad/internal/protocol"
	"somad/internal/server"
	"somad/internal/state"
	"somad/internal/tlsutil"
)

// daemonAction is a resolveDaemonOptions outcome: either run the server, or
// perform one "print (or write) and exit" action instead.
type daemonAction int

const (
	daemonActionRun daemonAction = iota
	daemonActionGenPSK
	daemonActionShowCert
)

// daemonOptions is the resolved, validated result of parsing `soma daemon`'s
// flags against their config-file defaults. resolveDaemonOptions builds one
// from a config and argv alone (no log.Fatal, no global state), so it can be
// tested directly; runServer is the sole place that turns a resolution
// failure into a fatal exit and acts on the result.
type daemonOptions struct {
	action daemonAction

	// Fields for daemonActionRun.
	idleTimeout time.Duration
	noTray      bool
	notify      bool
	quality     string
	listen      string
	tlsEnabled  bool
	certPath    string
	keyPath     string
	psk         string
	insecure    bool

	// genPSKPath is where daemonActionGenPSK should write the generated key.
	genPSKPath string

	// certFingerprint is the SHA-256 fingerprint daemonActionShowCert
	// should print alongside certPath.
	certFingerprint string
}

// daemonFlags holds the parsed `soma daemon` flags, before any file they
// name has been resolved.
type daemonFlags struct {
	idleTimeout time.Duration
	noTray      bool
	notify      bool
	quality     string
	listen      string
	tls         bool
	tlsCert     string
	tlsKey      string
	pskFile     string
	insecure    bool
	showCert    bool
	genPSK      bool
}

// parseDaemonFlags parses `soma daemon`'s flags, seeded from cfg's defaults
// so an explicit flag overrides the config file. Path-valued flags get the
// same "~/" expansion config.Load applies to the keys they mirror (a shell
// normally expands "~" for a flag, but a quoted value should still work).
func parseDaemonFlags(cfg *config.Config, args []string) (daemonFlags, error) {
	defaultIdleTimeout := server.DefaultIdleTimeout
	if cfg.Server.IdleTimeout != nil {
		defaultIdleTimeout = time.Duration(*cfg.Server.IdleTimeout)
	}
	defaultNoTray := cfg.Server.Tray != nil && !*cfg.Server.Tray

	var f daemonFlags
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: soma daemon [flags]")
		_, _ = fmt.Fprintln(fs.Output(), "Flags:")
		printFlagDefaults(fs)
	}
	fs.DurationVar(&f.idleTimeout, "idle-timeout", defaultIdleTimeout,
		"exit after this long with no clients and stopped playback (0 disables)")
	fs.BoolVar(&f.noTray, "no-tray", defaultNoTray,
		"do not show the system tray / menu-bar icon while the server runs")
	fs.BoolVar(&f.notify, "notify", boolVal(cfg.Server.Notify),
		"show a desktop notification when the playing track changes")
	fs.StringVar(&f.quality, "quality", str(cfg.Server.Quality),
		"preferred stream quality: highest, high, or low (falls back to the nearest available; default highest)")
	fs.StringVar(&f.listen, "listen", str(cfg.Server.Listen),
		"also listen for frontends on this TCP host:port (empty: Unix socket only)")
	fs.BoolVar(&f.tls, "tls", boolVal(cfg.Server.TLS),
		"serve the TCP listener over TLS (a certificate is generated when none is configured)")
	fs.StringVar(&f.tlsCert, "tls-cert", str(cfg.Server.TLSCert),
		"PEM certificate for the TCP listener (implies --tls; requires --tls-key)")
	fs.StringVar(&f.tlsKey, "tls-key", str(cfg.Server.TLSKey),
		"PEM private key belonging to --tls-cert")
	fs.StringVar(&f.pskFile, "psk-file", str(cfg.Server.PSKFile),
		"file holding the pre-shared key TCP clients must authenticate with")
	fs.BoolVar(&f.insecure, "insecure", boolVal(cfg.Server.Insecure),
		"serve a non-loopback --listen address even without TLS and a PSK")
	fs.BoolVar(&f.showCert, "show-cert", false,
		"print the TLS certificate path and fingerprint, then exit")
	fs.BoolVar(&f.genPSK, "gen-psk", false,
		`generate a random pre-shared key at --psk-file (or a "psk" file in the config directory when unset), then exit`)
	_ = fs.Parse(args)

	for _, p := range []*string{&f.tlsCert, &f.tlsKey, &f.pskFile} {
		expanded, err := config.ExpandHome(*p)
		if err != nil {
			return daemonFlags{}, err
		}
		*p = expanded
	}
	return f, nil
}

// resolveDaemonOptions parses `soma daemon`'s flags (seeded from cfg's
// defaults), validates them, and resolves the files they name — a TLS
// certificate pair (generating a self-signed one when none is configured),
// a PSK file — into a daemonOptions. It returns an error instead of calling
// log.Fatal for every failure that can occur during resolution: the
// --quality validation, the --tls-cert/--tls-key pairing rule, certificate
// preparation, certificate loading for --show-cert, and the PSK file read.
func resolveDaemonOptions(cfg *config.Config, args []string) (daemonOptions, error) {
	f, err := parseDaemonFlags(cfg, args)
	if err != nil {
		return daemonOptions{}, err
	}

	if f.genPSK {
		path := f.pskFile
		if path == "" {
			dir, err := config.Dir()
			if err != nil {
				return daemonOptions{}, fmt.Errorf("error resolving the config directory: %w", err)
			}
			path = filepath.Join(dir, "psk")
		}
		return daemonOptions{action: daemonActionGenPSK, genPSKPath: path}, nil
	}

	if f.quality != "" && !config.ValidQuality(f.quality) {
		return daemonOptions{}, fmt.Errorf("--quality (or server.quality in the config) must be one of %s (got %q)", config.QualityList(), f.quality)
	}

	certPath, keyPath := f.tlsCert, f.tlsKey
	if (certPath == "") != (keyPath == "") {
		return daemonOptions{}, errors.New("--tls-cert and --tls-key (or tls_cert/tls_key in the config) must be set together")
	}
	tlsEnabled := f.tls || certPath != ""
	// The certificate is resolved (and generated) even for --show-cert with
	// TLS not yet enabled: the user is pairing a client right now.
	if tlsEnabled || f.showCert {
		if certPath, keyPath, err = ensureCertPair(certPath, keyPath, f.listen); err != nil {
			return daemonOptions{}, fmt.Errorf("error preparing the TLS certificate: %w", err)
		}
	}
	if f.showCert {
		_, fingerprint, err := tlsutil.ServerTLSConfig(certPath, keyPath)
		if err != nil {
			return daemonOptions{}, fmt.Errorf("error loading the TLS certificate: %w", err)
		}
		return daemonOptions{action: daemonActionShowCert, certPath: certPath, certFingerprint: fingerprint}, nil
	}

	psk := str(cfg.Server.PSK)
	if f.pskFile != "" {
		if psk, err = readPSKFile(f.pskFile); err != nil {
			return daemonOptions{}, fmt.Errorf("error reading the PSK file: %w", err)
		}
	}

	return daemonOptions{
		action:      daemonActionRun,
		idleTimeout: f.idleTimeout,
		noTray:      f.noTray,
		notify:      f.notify,
		quality:     f.quality,
		listen:      f.listen,
		tlsEnabled:  tlsEnabled,
		certPath:    certPath,
		keyPath:     keyPath,
		psk:         psk,
		insecure:    f.insecure,
	}, nil
}

// runServer runs the playback daemon: it owns audio, the channel catalog,
// persisted state, and MPRIS, and serves clients on the Unix socket (and,
// when configured, TCP).
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

	opts, err := resolveDaemonOptions(cfg, args)
	if err != nil {
		log.Fatalf("%v", err)
	}

	switch opts.action {
	case daemonActionGenPSK:
		if err := writeGeneratedPSK(opts.genPSKPath); err != nil {
			log.Fatalf("error generating the PSK file: %v", err)
		}
		fmt.Printf("generated a pre-shared key at %s\n", opts.genPSKPath)
		return
	case daemonActionShowCert:
		fmt.Printf("certificate: %s\nfingerprint: %s\n", opts.certPath, opts.certFingerprint)
		return
	}

	listeners, cleanup, err := openListeners(opts)
	if errors.Is(err, server.ErrAlreadyRunning) {
		// A concurrent auto-spawn lost the race; the winner serves everyone.
		log.Print("soma daemon already running, exiting")
		return
	}
	if err != nil {
		log.Fatalf("%v", err)
	}

	// cleanup must run on every path below, including the fatal ones, so it
	// is called explicitly rather than deferred (log.Fatalf skips defers).
	srv, tr, err := buildServer(cfg, opts)
	if err == nil {
		if err = serve(srv, tr, listeners); err != nil {
			err = fmt.Errorf("server error: %w", err)
		}
	}
	cleanup()
	if err != nil {
		log.Fatalf("%v", err)
	}
	// Shutdown's player.Stop fades out asynchronously; give it a moment so
	// the audio doesn't cut off hard.
	time.Sleep(400 * time.Millisecond)
}

// openListeners binds the Unix socket and, when configured, the TCP
// listener. The socket is bound before the (potentially slow) audio init in
// buildServer: a bound socket is the readiness signal spawning clients poll
// for, and taking the lock early makes concurrent auto-spawns exit quickly.
// Connections arriving before Run starts simply queue in the listen
// backlog. The returned cleanup releases the socket and lock; it is safe to
// call after Run has returned.
func openListeners(opts daemonOptions) ([]net.Listener, func(), error) {
	socketPath := protocol.SocketPath()
	ln, cleanup, err := server.Listen(socketPath)
	if err != nil {
		if errors.Is(err, server.ErrAlreadyRunning) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("error starting server: %w", err)
	}
	log.Printf("soma daemon %s listening on %s", version, socketPath)

	listeners := []net.Listener{ln}
	if opts.listen != "" {
		tcpLn, err := listenTCP(opts)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("error starting the TCP listener: %w", err)
		}
		listeners = append(listeners, tcpLn)
	}
	return listeners, cleanup, nil
}

// buildServer assembles the server from its collaborators: the audio
// player, persisted state, MPRIS, the tray, and the Last.fm scrobbler. The
// tray is nil when disabled, unsupported, or when no GUI is present (a
// headless host), so the server still runs anywhere.
func buildServer(cfg *config.Config, opts daemonOptions) (*server.Server, *tray.Tray, error) {
	player, err := audio.NewPlayer(userAgent())
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing the audio player: %w", err)
	}
	appState, err := state.LoadState()
	if err != nil {
		return nil, nil, fmt.Errorf("error loading state: %w", err)
	}
	mpris, err := platform.NewMPRIS()
	if err != nil {
		// MPRIS is optional, continue without it
		log.Printf("warning: MPRIS initialization failed: %v", err)
	}
	var tr *tray.Tray
	if !opts.noTray && tray.Available() {
		tr = tray.New()
	}
	scrobbler, reloadLastfmSession := setUpLastfm(cfg)

	srv := server.New(server.Config{
		Version:             version,
		UserAgent:           userAgent(),
		Player:              player,
		State:               appState,
		MPRIS:               mpris,
		Tray:                tr,
		IdleTimeout:         opts.idleTimeout,
		PSK:                 opts.psk,
		Quality:             opts.quality,
		Notify:              opts.notify,
		Scrobbler:           scrobbler,
		ReloadLastfmSession: reloadLastfmSession,
	})
	return srv, tr, nil
}

// serve runs the server until it shuts down (a signal, the idle timer, the
// tray's Quit item, or a shutdown RPC). The tray owns the process's native
// GUI run loop and must run on the main goroutine, so with a tray the
// connections are served on a goroutine and the tray blocks; srv.Shutdown
// stops the tray, which unblocks Run. Without a tray, Run blocks directly.
func serve(srv *server.Server, tr *tray.Tray, listeners []net.Listener) error {
	// The server must survive its spawning terminal closing; SIGINT/SIGTERM
	// shut it down cleanly.
	signal.Ignore(syscall.SIGHUP)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Shutdown()
	}()

	if tr == nil {
		return srv.Run(listeners...)
	}
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- srv.Run(listeners...)
		srv.Shutdown() // idempotent; unblocks the tray on any exit path
	}()
	tr.Run(nil)
	return <-runErrCh
}

// setUpLastfm builds the Last.fm scrobbler from the config's lastfm.*
// keys (see internal/config), when both api_key and api_secret are set;
// otherwise scrobbling is disabled entirely and both return values are
// nil. The initial session key is resolveLastfmSession's result at
// startup; the returned function recomputes it for the reloadLastfm RPC
// that "soma lastfm login" triggers after a successful login.
func setUpLastfm(cfg *config.Config) (server.Scrobbler, func() (string, error)) {
	apiKey := str(cfg.Lastfm.APIKey)
	if apiKey == "" {
		return nil, nil
	}
	apiSecret := str(cfg.Lastfm.APISecret)
	resolveSession := func() (string, error) { return resolveLastfmSession(cfg) }
	sessionKey, err := resolveSession()
	if err != nil {
		log.Printf("warning: could not read the last.fm session: %v", err)
	}
	return lastfm.New(apiKey, apiSecret, sessionKey, userAgent()), resolveSession
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

// pskBytes is the amount of randomness in a generated pre-shared key.
const pskBytes = 32

// generatePSK returns pskBytes of randomness, hex-encoded so the resulting
// file is a single printable line suitable for --psk-file.
func generatePSK() (string, error) {
	b := make([]byte, pskBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writeGeneratedPSK generates a fresh pre-shared key and writes it to path
// at 0600, refusing to overwrite an existing file (the same exclusive
// create config.EnsureTemplate uses, so a hand-edited or previously
// generated file can never be clobbered).
func writeGeneratedPSK(path string) error {
	psk, err := generatePSK()
	if err != nil {
		return err
	}
	created, err := atomicfile.CreateExclusive(path, 0o600, func(w io.Writer) error {
		_, err := fmt.Fprintln(w, psk)
		return err
	})
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("%s already exists; refusing to overwrite it", path)
	}
	return nil
}

// checkTCPSecurity rejects a non-loopback TCP listener that lacks TLS or a
// PSK, unless the user explicitly opted out with --insecure. Loopback binds
// only warn (in listenTCP): the machine boundary already limits exposure,
// like the Unix socket.
func checkTCPSecurity(opts daemonOptions) error {
	if opts.insecure || isLoopbackAddr(opts.listen) {
		return nil
	}
	if opts.psk == "" {
		return fmt.Errorf("refusing to serve %s without authentication: anyone who can reach the port would control playback and could shut the daemon down; set a PSK (--psk-file or server.psk) or pass --insecure to serve it open", opts.listen)
	}
	if !opts.tlsEnabled {
		return fmt.Errorf("refusing to serve %s with a PSK but no TLS: without encryption an attacker on the network can hijack authenticated connections; pass --tls or --insecure", opts.listen)
	}
	return nil
}

// isLoopbackAddr reports whether a listen address can only be reached from
// this machine. An empty host (":5454") binds all interfaces.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// maxTCPConns caps simultaneously open TCP connections; beyond it new peers
// wait in the listen backlog. Resource exhaustion, not key guessing, is the
// realistic threat on an exposed listener (the HMAC challenge–response makes
// online guessing impractical), and this bounds it.
const maxTCPConns = 64

// listenTCP binds the remote-frontend listener, wrapping it in TLS when
// enabled, and logs what protections it runs with — including prominent
// warnings for the combinations that leave it open.
func listenTCP(opts daemonOptions) (net.Listener, error) {
	if err := checkTCPSecurity(opts); err != nil {
		return nil, err
	}
	// The zero ListenConfig enables TCP keepalive on accepted connections,
	// which is what reaps dead peers after hello (see server.handshakeTimeout).
	tcpLn, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", opts.listen)
	if err != nil {
		return nil, err
	}
	// Cap open TCP connections before TLS so unfinished handshakes count
	// too; a handful of frontends is the realistic load.
	tcpLn = server.LimitListener(tcpLn, maxTCPConns)
	if opts.tlsEnabled {
		tlsCfg, fingerprint, err := tlsutil.ServerTLSConfig(opts.certPath, opts.keyPath)
		if err != nil {
			_ = tcpLn.Close()
			return nil, err
		}
		tcpLn = tls.NewListener(tcpLn, tlsCfg)
		log.Printf("listening on tcp://%s with TLS, certificate fingerprint %s", tcpLn.Addr(), fingerprint)
	} else {
		log.Printf("WARNING: the TCP listener on %s is unencrypted (no TLS); anyone on the network can observe it", tcpLn.Addr())
	}
	if opts.psk == "" {
		log.Printf("WARNING: the TCP listener on %s requires no authentication (no PSK); anyone who can reach it controls playback", tcpLn.Addr())
	}
	return tcpLn, nil
}
