package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"somad/internal/config"
	"somad/internal/lastfm"
	"somad/internal/state"
)

// lastfmLoginInput is where "soma lastfm login" reads the Enter keypress
// that continues past the "authorize in your browser" step. A variable so
// tests can supply scripted input instead of blocking on a real terminal.
var lastfmLoginInput io.Reader = os.Stdin

// openURLFunc attempts to open a URL in the user's browser; failures are
// ignored by the caller, since the URL is also printed for opening by
// hand. A variable so tests can stub it out rather than actually launching
// a browser.
var openURLFunc = openURL

// openURL shells out to the platform's "open a URL" command.
func openURL(rawURL string) error {
	cmd := "xdg-open"
	if runtime.GOOS == "darwin" {
		cmd = "open"
	}
	// #nosec G204 -- rawURL is built by lastfm.Client.AuthURL from our own
	// api_key and a token we just requested, not from unsanitized user
	// input, and is passed as a single argument, not through a shell.
	return exec.CommandContext(context.Background(), cmd, rawURL).Start()
}

// runLastfm dispatches soma lastfm's subcommands.
func runLastfm(args []string) {
	usage := "usage: soma lastfm <login|logout|status>"
	if len(args) == 0 {
		fail("%s", usage)
	}
	switch args[0] {
	case "login":
		runLastfmLogin(args[1:])
	case "logout":
		runLastfmLogout(args[1:])
	case "status":
		runLastfmStatus(args[1:])
	default:
		fail("%s", usage)
	}
}

// lastfmClientFromConfig loads the config and builds a lastfm.Client from
// its lastfm.api_key/api_secret, failing with a helpful message (pointing
// at where to create a key pair) when they are not set.
func lastfmClientFromConfig() *lastfm.Client {
	cfg, err := config.Load()
	if err != nil {
		fail("error loading config: %v", err)
	}
	apiKey, apiSecret := str(cfg.Lastfm.APIKey), str(cfg.Lastfm.APISecret)
	if apiKey == "" || apiSecret == "" {
		fail("lastfm.api_key and lastfm.api_secret must be set in the config file first; " +
			"create a key pair at https://www.last.fm/api/account/create")
	}
	return lastfm.New(apiKey, apiSecret, "", userAgent())
}

// runLastfmLogin runs the Last.fm desktop auth flow (see
// https://www.last.fm/api/desktopauth): fetch a token, send the user to
// authorize it in a browser, exchange it for a session key once they
// confirm, and persist that key (internal/state's lastfm.json). It then
// asks a locally running daemon to reload it (best-effort: a daemon started
// afterwards reads the file at startup anyway).
func runLastfmLogin(args []string) {
	if len(args) != 0 {
		fail("usage: soma lastfm login")
	}
	c := lastfmClientFromConfig()

	token, err := c.GetToken()
	if err != nil {
		fail("could not get a last.fm auth token: %v", err)
	}

	authURL := c.AuthURL(token)
	fmt.Printf("Open this URL to authorize soma with your last.fm account:\n\n  %s\n\n", authURL)
	_ = openURLFunc(authURL) // best-effort; the printed URL works either way

	fmt.Print("Press Enter after authorizing... ")
	_, _ = bufio.NewReader(lastfmLoginInput).ReadString('\n')

	sessionKey, err := c.GetSession(token)
	if err != nil {
		fail("could not obtain a last.fm session (did you authorize the URL above?): %v", err)
	}
	if err := state.SaveLastfmSession(sessionKey); err != nil {
		fail("could not save the last.fm session: %v", err)
	}

	reloadRunningDaemon()
	fmt.Println("Logged in to last.fm.")
}

// runLastfmLogout removes the persisted session and asks a locally running
// daemon to stop scrobbling under it.
func runLastfmLogout(args []string) {
	if len(args) != 0 {
		fail("usage: soma lastfm logout")
	}
	if err := state.ClearLastfmSession(); err != nil {
		fail("%v", err)
	}
	reloadRunningDaemon()
	fmt.Println("Logged out of last.fm.")
}

// reloadRunningDaemon best-effort asks a locally reachable daemon to reload
// its Last.fm session (see the reloadLastfm RPC), so a login or logout
// takes effect immediately instead of waiting for the next restart. It
// never fails the caller: no daemon running, or one that is unreachable,
// is not an error here — state was already persisted successfully.
func reloadRunningDaemon() {
	c, err := tryDialServer()
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	if err := c.ReloadLastfm(); err != nil {
		fmt.Printf("soma: could not tell the running daemon to reload: %v\n", err)
	}
}

// lastfmStatusResult is the machine-readable form of `soma lastfm status
// --json`.
type lastfmStatusResult struct {
	Configured bool `json:"configured"`
	LoggedIn   bool `json:"loggedIn"`
}

// runLastfmStatus reports whether Last.fm scrobbling is configured
// (lastfm.api_key/api_secret set) and logged in (a session key is
// available, from the config override or internal/state's lastfm.json).
func runLastfmStatus(args []string) {
	args, jsonOut := parseJSONFlag("lastfm status", "soma lastfm status [--json]", args)
	if len(args) != 0 {
		fail("usage: soma lastfm status [--json]")
	}

	cfg, err := config.Load()
	if err != nil {
		fail("error loading config: %v", err)
	}
	configured := str(cfg.Lastfm.APIKey) != "" && str(cfg.Lastfm.APISecret) != ""

	sessionKey := str(cfg.Lastfm.SessionKey)
	if sessionKey == "" {
		sessionKey, err = state.LoadLastfmSession()
		if err != nil {
			fail("%v", err)
		}
	}
	loggedIn := sessionKey != ""

	if jsonOut {
		printJSON(lastfmStatusResult{Configured: configured, LoggedIn: loggedIn})
		return
	}
	switch {
	case !configured:
		fmt.Println("last.fm: not configured (set lastfm.api_key and lastfm.api_secret in the config file)")
	case !loggedIn:
		fmt.Println(`last.fm: configured, not logged in (run "soma lastfm login")`)
	default:
		fmt.Println("last.fm: configured and logged in")
	}
}
