package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"somad/internal/config"
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
		// rather than silently ignoring them, naming the offending flag —
		// "put it after the subcommand" would be wrong advice for the
		// TUI-only --shutdown-on-exit.
		var set []string
		fs.Visit(func(f *flag.Flag) { set = append(set, "--"+f.Name) })
		if len(set) > 0 {
			fail("%s does not apply to the daemon; daemon flags go after the subcommand: soma daemon [flags]", strings.Join(set, ", "))
		}
		runServer(rest[1:])
		return
	}

	// Completion scripts run `soma completion channels` on every Tab press;
	// dispatch before the config load so a broken config cannot break
	// completion. Like the word forms above, it ignores the connection flags.
	if len(rest) > 0 && rest[0] == "completion" {
		runCompletion(rest[1:])
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
		runPlayRelative(1, "next", rest[1:])
	case "prev", "previous":
		runPlayRelative(-1, "prev", rest[1:])
	case "pause":
		runPause(rest[1:])
	case "stop":
		runStop(rest[1:])
	case "status":
		runStatus(rest[1:])
	case "volume":
		runVolume(rest[1:])
	case "history":
		runHistory(rest[1:])
	case "lastfm":
		runLastfm(rest[1:])
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
  soma play [--json] [channel]
                                 play a channel by ID or name, or resume the
                                 last played channel (spawns the server if needed)
  soma list [--json]          list all channels (favorites first, marked *)
  soma favorite [--json] <channel>
                                 toggle a channel's favorite flag
  soma next [--json]          play the next channel (favorites first, wraps)
  soma prev [--json]          play the previous channel
  soma pause [--json]         toggle pause (reconnects the live stream on unpause)
  soma stop [--json]          stop playback
  soma stop --in <duration>   stop after this long instead (e.g. 45m; a sleep
                                 timer owned by the daemon, so it fires even
                                 after this command exits; replaces any timer
                                 already pending)
  soma stop --cancel          cancel a pending sleep timer without stopping
  soma status [--json]        show what is playing
  soma volume [--json] [<0-100>|+n|-n]
                                 show, set, or adjust the playback volume
  soma volume mute [--json]   toggle mute, restoring the previous level
  soma history [--json] [-n N] [channel]
                                 show recent now-playing titles, newest first
                                 (all channels, or one when given; -n bounds
                                  how many, default 20)
  soma lastfm login           authorize soma with your last.fm account and
                                 save the session (requires lastfm.api_key
                                 and lastfm.api_secret in the config file)
  soma lastfm logout          remove the saved last.fm session
  soma lastfm status [--json] show whether last.fm scrobbling is configured
                                 and logged in
  soma daemon [flags]         run the playback server in the foreground
                                 (--no-tray hides the tray / menu-bar icon;
                                  --notify shows a desktop notification on
                                  track change; --quality prefers a stream
                                  quality; --listen <host:port> also serves
                                  frontends over TCP, --tls encrypts it,
                                  --psk-file requires a pre-shared key,
                                  --gen-psk generates one,
                                  --show-cert prints the TLS certificate
                                  fingerprint)
  soma daemon stop            shut down the playback server
  soma completion <bash|zsh>  print a completion script for the given shell
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
    idle_timeout: 5m       # exit after this long idle (default "0": never)
    tray: false            # hide the tray / menu-bar icon
    notify: true           # desktop notification on track change (default false)
    quality: high          # preferred stream quality (default "highest")
    listen: ":5454"        # also serve frontends over TCP
    tls: true              # ...encrypted (auto-generated certificate)
    psk_file: ~/.config/somad/psk  # ...and authenticated (soma daemon --gen-psk writes it)
  client:
    server: "myserver:5454"
    tls_fingerprint: "sha256:..."
    psk_file: ~/.config/somad/psk
  tui:
    shutdown_on_exit: true
  lastfm:
    api_key: your-api-key    # from https://www.last.fm/api/account/create
    api_secret: your-api-secret
    # session_key is normally left unset here: "soma lastfm login" saves it
    # separately instead of editing this file.
`, path)
	}
}
