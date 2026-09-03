package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"somad/internal/channels"
	"somad/internal/client"
	"somad/internal/protocol"
	"somad/internal/state"
)

// catalogWait bounds how long CLI commands wait for a freshly spawned
// server to finish loading the channel catalog.
const catalogWait = 15 * time.Second

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "soma: "+format+"\n", args...)
	os.Exit(1)
}

// ensureServer connects for a command that does not interrupt playback (it
// spawns the server if needed but leaves a running, playing one in place even
// when its version differs from ours, so the music keeps going).
func ensureServer() *client.Client {
	c, _, err := client.EnsureServer(endpoint, version)
	if err != nil {
		fail("%v", err)
	}
	return c
}

// ensureServerForPlayback connects for a command that changes the stream. Since
// that interrupts playback anyway, an out-of-date local server is restarted onto
// our version first, so the command runs against the up-to-date binary.
func ensureServerForPlayback() *client.Client {
	c, _, err := client.EnsureServerForPlayback(endpoint, version)
	if err != nil {
		fail("%v", err)
	}
	return c
}

// dialServer connects to a running server without spawning one, returning its
// reported version. The last return value is false when no server is listening
// locally; an unreachable remote server is an error instead, because "not
// running" is not something this side can know or fix.
func dialServer() (*client.Client, string, bool) {
	c, err := client.DialEndpoint(endpoint)
	if err != nil {
		if endpoint.IsLocal() {
			return nil, "", false
		}
		fail("cannot reach the soma daemon at %s: %v", endpoint, err)
	}
	hr, err := c.Hello(version)
	if err != nil {
		_ = c.Close()
		fail("%v", err)
	}
	return c, hr.ServerVersion, true
}

// restartForUpgrade restarts an out-of-date local server onto our version, for
// a command that is about to interrupt playback anyway. It returns c unchanged
// when the server already runs our version — or is remote, where a version-
// skewed server is tolerated (the hello handshake already checked that it
// speaks our protocol version).
func restartForUpgrade(c *client.Client, serverVersion string) *client.Client {
	if !client.VersionSkewed(version, serverVersion) || !endpoint.IsLocal() {
		return c
	}
	nc, _, err := client.Restart(c, endpoint, version)
	if err != nil {
		fail("%v", err)
	}
	return nc
}

// waitForCatalog fetches the channel catalog, waiting out a fresh server's
// initial load.
func waitForCatalog(c *client.Client) protocol.ChannelsPayload {
	deadline := time.Now().Add(catalogWait)
	for {
		payload, err := c.Channels()
		if err != nil {
			fail("%v", err)
		}
		if len(payload.Channels) > 0 {
			return payload
		}
		if payload.Error != "" {
			fail("failed to load channels: %s", payload.Error)
		}
		if time.Now().After(deadline) {
			fail("timed out waiting for the channel list")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// resolveChannel finds a channel by exact ID, or by unique case-insensitive
// substring of its ID or title.
func resolveChannel(catalog []channels.Channel, query string) (channels.Channel, error) {
	if ch, ok := findChannelByID(catalog, query); ok {
		return ch, nil
	}
	q := strings.ToLower(query)
	var matches []channels.Channel
	for _, ch := range catalog {
		if strings.Contains(strings.ToLower(ch.ID), q) || strings.Contains(strings.ToLower(ch.Title), q) {
			matches = append(matches, ch)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return channels.Channel{}, fmt.Errorf("no channel matches %q", query)
	default:
		ids := make([]string, len(matches))
		for i, ch := range matches {
			ids[i] = fmt.Sprintf("%s (%s)", ch.ID, ch.Title)
		}
		return channels.Channel{}, fmt.Errorf("%q is ambiguous, matches: %s", query, strings.Join(ids, ", "))
	}
}

func runPlay(args []string) {
	args, jsonOut := parseJSONFlag("play", "soma play [--json] [channel-id-or-name]", args)
	if len(args) > 1 {
		fail("usage: soma play [--json] [channel-id-or-name]")
	}
	c := ensureServerForPlayback()
	defer func() { _ = c.Close() }()

	payload := waitForCatalog(c)
	var ch channels.Channel
	if len(args) == 0 {
		// Without an argument, resume the last played channel.
		if payload.LastChannelID == "" {
			fail("no previously played channel; usage: soma play [--json] <channel-id-or-name>")
		}
		var ok bool
		ch, ok = findChannelByID(payload.Channels, payload.LastChannelID)
		if !ok {
			fail("last played channel %q is not in the channel list", payload.LastChannelID)
		}
	} else {
		var err error
		ch, err = resolveChannel(payload.Channels, args[0])
		if err != nil {
			fail("%v", err)
		}
	}

	st, err := c.Play(ch.ID)
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(st)
		return
	}
	fmt.Printf("Playing: %s\n", st.ChannelTitle)
}

// parseJSONFlag parses a client command's arguments, which may lead with
// --json for machine-readable output, and returns the positional rest.
//
// It is not used for soma volume: the flag package treats any argument
// starting with "-" as a flag, and volume's own positional argument can be
// a relative decrease like "-30" — stripJSONFlag handles that command
// instead.
func parseJSONFlag(name, usageLine string, args []string) (rest []string, jsonOut bool) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() { _, _ = fmt.Fprintf(fs.Output(), "usage: %s\n", usageLine) }
	j := fs.Bool("json", false, "print machine-readable JSON")
	_ = fs.Parse(args)
	return fs.Args(), *j
}

// stripJSONFlag strips a leading --json argument, for soma volume, whose own
// positional argument may itself look like a flag (a relative decrease such
// as "-30"), which rules out flag.FlagSet-based parsing (see parseJSONFlag).
func stripJSONFlag(args []string) (rest []string, jsonOut bool) {
	if len(args) > 0 && args[0] == "--json" {
		return args[1:], true
	}
	return args, false
}

// runList prints the channel catalog, favorites first and marked with a
// star, one channel per line for browsing and scripting. With --json, it
// prints the same catalog as a JSON array for scripts to parse.
func runList(args []string) {
	args, jsonOut := parseJSONFlag("list", "soma list [--json]", args)
	if len(args) != 0 {
		fail("usage: soma list [--json]")
	}

	c := ensureServer()
	defer func() { _ = c.Close() }()

	payload := waitForCatalog(c)
	if jsonOut {
		printJSON(channelListEntries(payload))
		return
	}
	fmt.Print(formatChannelList(payload))
}

// formatChannelList renders the catalog as one line per channel: a favorite
// marker, then aligned ID, title, and genre columns. The ID leads so shell
// pipelines can cut it out easily.
func formatChannelList(payload protocol.ChannelsPayload) string {
	fav := state.FavoriteSet(payload.Favorites)
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, ch := range payload.Channels {
		marker := " "
		if fav[ch.ID] {
			marker = "*"
		}
		_, _ = fmt.Fprintf(w, "%s %s\t%s\t%s\n", marker, ch.ID, ch.Title, ch.Genre)
	}
	_ = w.Flush()
	return b.String()
}

// channelListEntry is the machine-readable form of one catalog row for
// `soma list --json`.
type channelListEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Genre    string `json:"genre"`
	Favorite bool   `json:"favorite"`
}

// channelListEntries converts the catalog payload to its JSON list form,
// preserving the server's favorites-first ordering.
func channelListEntries(payload protocol.ChannelsPayload) []channelListEntry {
	fav := state.FavoriteSet(payload.Favorites)
	entries := make([]channelListEntry, len(payload.Channels))
	for i, ch := range payload.Channels {
		entries[i] = channelListEntry{ID: ch.ID, Title: ch.Title, Genre: ch.Genre, Favorite: fav[ch.ID]}
	}
	return entries
}

// runFavorite toggles a channel's favorite flag, so favorites can be managed
// without opening the TUI. With --json, it prints the toggle result instead
// of the human-readable message.
func runFavorite(args []string) {
	args, jsonOut := parseJSONFlag("favorite", "soma favorite [--json] <channel-id-or-name>", args)
	if len(args) != 1 {
		fail("usage: soma favorite [--json] <channel-id-or-name>")
	}
	c := ensureServer()
	defer func() { _ = c.Close() }()

	payload := waitForCatalog(c)
	ch, err := resolveChannel(payload.Channels, args[0])
	if err != nil {
		fail("%v", err)
	}
	favorites, err := c.ToggleFavorite(ch.ID)
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(favoriteResult{ChannelID: ch.ID, Title: ch.Title, Favorite: slices.Contains(favorites, ch.ID)})
		return
	}
	fmt.Println(favoriteMessage(favorites, ch))
}

// favoriteResult is the machine-readable form of a favorite toggle for
// `soma favorite --json`.
type favoriteResult struct {
	ChannelID string `json:"channelId"`
	Title     string `json:"title"`
	Favorite  bool   `json:"favorite"`
}

// favoriteMessage reports which way a favorite toggle went, based on the
// favorites list the server returned.
func favoriteMessage(favorites []string, ch channels.Channel) string {
	if slices.Contains(favorites, ch.ID) {
		return "Favorited: " + ch.Title
	}
	return "Unfavorited: " + ch.Title
}

// findChannelByID returns the channel with the exact ID, if present.
func findChannelByID(catalog []channels.Channel, id string) (channels.Channel, bool) {
	for _, ch := range catalog {
		if ch.ID == id {
			return ch, true
		}
	}
	return channels.Channel{}, false
}

// runPlayRelative plays the next (+1) or previous (-1) channel relative to
// the current or last played one, in catalog order (favorites first). name is
// the command word ("next" or "prev") used in usage and error messages.
func runPlayRelative(delta int, name string, args []string) {
	usage := "soma " + name + " [--json]"
	args, jsonOut := parseJSONFlag(name, usage, args)
	if len(args) != 0 {
		fail("usage: %s", usage)
	}

	c := ensureServerForPlayback()
	defer func() { _ = c.Close() }()

	// A freshly spawned server may still be loading the catalog.
	waitForCatalog(c)

	st, err := c.PlayRelative(delta)
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(st)
		return
	}
	fmt.Printf("Playing: %s\n", st.ChannelTitle)
}

// runPause toggles between stopped and playing. Live radio has no real
// pause: unpausing reconnects to the live stream of the last channel. With
// --json, it prints the resulting protocol.PlaybackState instead of the
// human-readable message.
func runPause(args []string) {
	args, jsonOut := parseJSONFlag("pause", "soma pause [--json]", args)
	if len(args) != 0 {
		fail("usage: soma pause [--json]")
	}

	c, serverVersion, running := dialServer()
	if !running {
		if jsonOut {
			printJSON(statusSnapshot())
			return
		}
		fmt.Println("soma: not playing (server not running)")
		return
	}
	defer func() { _ = c.Close() }()

	if client.VersionSkewed(version, serverVersion) && endpoint.IsLocal() {
		// Pausing interrupts playback anyway, so upgrade the server now. The
		// fresh server starts stopped: if music was playing, that stopped state
		// *is* the pause; if it was already paused, unpausing means resuming the
		// last channel on the new server.
		wasPlaying := false
		if st, err := c.Status(); err == nil {
			wasPlaying = st.Status != protocol.StatusStopped
		}
		c = restartForUpgrade(c, serverVersion)
		if wasPlaying {
			if jsonOut {
				st, err := c.Status()
				if err != nil {
					fail("%v", err)
				}
				printJSON(st)
				return
			}
			fmt.Println("Paused")
			return
		}
	}

	st, err := c.PlayPause()
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(st)
		return
	}
	if st.Status == protocol.StatusStopped {
		fmt.Println("Paused")
	} else {
		fmt.Printf("Playing: %s\n", st.ChannelTitle)
	}
}

// runStop stops playback, or with --in <duration> arms a sleep timer that
// stops it after that long instead of immediately (the daemon owns the
// timer, so it survives this process exiting). --cancel drops a pending
// sleep timer without stopping now. With --json, it prints the resulting
// protocol.PlaybackState instead of the human-readable message.
func runStop(args []string) {
	usage := "soma stop [--json] [--in <duration> | --cancel]"
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	fs.Usage = func() { _, _ = fmt.Fprintf(fs.Output(), "usage: %s\n", usage) }
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	in := fs.String("in", "", "stop after this long instead of immediately, e.g. 45m (replaces any pending timer)")
	cancel := fs.Bool("cancel", false, "cancel a pending sleep timer without stopping now")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fail("usage: %s", usage)
	}
	if *in != "" && *cancel {
		fail("--in and --cancel are mutually exclusive")
	}
	var delay time.Duration
	if *in != "" {
		var err error
		delay, err = time.ParseDuration(*in)
		if err != nil {
			fail("invalid --in duration: %v", err)
		}
	}

	c, serverVersion, running := dialServer()
	if !running {
		if *jsonOut {
			printJSON(statusSnapshot())
			return
		}
		fmt.Println("soma: not playing (server not running)")
		return
	}
	defer func() { _ = c.Close() }()

	var st protocol.PlaybackState
	var err error
	switch {
	case *cancel:
		st, err = c.CancelStop()
	case *in != "":
		// Arming or replacing a sleep timer does not interrupt playback, so
		// (unlike an immediate stop) an out-of-date local server is left
		// alone rather than restarted onto our version.
		st, err = c.StopIn(delay)
	default:
		// Stopping interrupts playback anyway, so upgrade an out-of-date server now;
		// the fresh server starts stopped, which is the state stop leaves us in.
		c = restartForUpgrade(c, serverVersion)
		st, err = c.Stop()
	}
	if err != nil {
		fail("%v", err)
	}
	if *jsonOut {
		printJSON(st)
		return
	}
	switch {
	case *cancel:
		fmt.Println("Sleep timer canceled")
	case *in != "":
		fmt.Printf("Stopping in %s\n", *in)
	default:
		fmt.Println("Stopped")
	}
}

// runStatus prints the playback state, as JSON with --json so status bars
// and scripts don't have to parse the human-readable output.
func runStatus(args []string) {
	args, jsonOut := parseJSONFlag("status", "soma status [--json]", args)
	if len(args) != 0 {
		fail("usage: soma status [--json]")
	}
	if jsonOut {
		printJSON(statusSnapshot())
		return
	}

	c, _, running := dialServer()
	if !running {
		fmt.Println("soma: stopped (server not running)")
		return
	}
	defer func() { _ = c.Close() }()

	st, err := c.Status()
	if err != nil {
		fail("%v", err)
	}
	switch st.Status {
	case protocol.StatusPlaying:
		fmt.Printf("Playing: %s\n", st.ChannelTitle)
		if st.TrackTitle != "" {
			fmt.Printf("Track:   %s\n", st.TrackTitle)
		}
	case protocol.StatusConnecting:
		fmt.Printf("Connecting: %s\n", st.ChannelTitle)
	case protocol.StatusReconnecting:
		fmt.Printf("Reconnecting (attempt %d): %s\n", st.ReconnectAttempt, st.ChannelTitle)
	default:
		fmt.Println("Stopped")
	}
	if st.StreamError != "" {
		fmt.Printf("Error:   %s\n", st.StreamError)
	}
	fmt.Printf("Volume:  %d%%\n", volumePercent(st.Volume))
	if line := sleepTimerLine(st.StopAt); line != "" {
		fmt.Print(line)
	}
}

// sleepTimerLine formats a pending sleep-timer stop (protocol.PlaybackState.
// StopAt, an RFC 3339 timestamp, armed by "soma stop --in") as
// "Sleep:   in 42m\n", or "" when none is pending or the timestamp fails to
// parse.
func sleepTimerLine(stopAt string) string {
	if stopAt == "" {
		return ""
	}
	at, err := time.Parse(time.RFC3339, stopAt)
	if err != nil {
		return ""
	}
	remaining := time.Until(at)
	if remaining < 0 {
		remaining = 0
	}
	if remaining < time.Minute {
		return fmt.Sprintf("Sleep:   in %ds\n", int(remaining.Round(time.Second).Seconds()))
	}
	return fmt.Sprintf("Sleep:   in %dm\n", int(remaining.Round(time.Minute).Minutes()))
}

// statusSnapshot returns the playback state for --json consumers. It never
// exits on an unreachable server: a polling status bar needs parseable
// output on every tick, not exit 1 with a message on stderr.
func statusSnapshot() protocol.PlaybackState {
	c, err := tryDialServer()
	if err != nil {
		st := protocol.PlaybackState{Status: protocol.StatusStopped}
		if endpoint.IsLocal() {
			// No local server means stopped; the persisted volume is what
			// the next server will use, so the snapshot is complete.
			if s, err := state.LoadState(); err == nil {
				st.Volume = s.GetVolume()
			}
			return st
		}
		// An unreachable remote server may be stopped, down, or cut off —
		// this side cannot tell, so report stopped with the error attached.
		st.StreamError = err.Error()
		return st
	}
	defer func() { _ = c.Close() }()

	st, err := c.Status()
	if err != nil {
		return protocol.PlaybackState{Status: protocol.StatusStopped, StreamError: err.Error()}
	}
	return st
}

// tryDialServer dials and greets a running server, without spawning one and
// without exiting on failure.
func tryDialServer() (*client.Client, error) {
	c, err := client.DialEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if _, err := c.Hello(version); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// volumePercent converts a volume fraction in [0, 1] to a rounded percentage
// for display.
func volumePercent(v float64) int {
	return int(v*100 + 0.5)
}

// printJSON writes v as a single JSON line.
func printJSON(v any) {
	out, err := json.Marshal(v)
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(string(out))
}

// runVolume shows the volume when called without an argument, sets it for an
// absolute percentage, adjusts it for an explicitly signed one, or toggles
// mute. With --json, it prints a protocol.PlaybackState instead of the
// human-readable line, in every case.
func runVolume(args []string) {
	args, jsonOut := stripJSONFlag(args)
	if len(args) == 0 {
		showVolume(jsonOut)
		return
	}
	if len(args) != 1 {
		fail("usage: soma volume [--json] [<0-100> | +<n> | -<n> | mute]")
	}
	if args[0] == "mute" {
		runVolumeMute(jsonOut)
		return
	}
	pct, relative, err := parseVolumeArg(args[0])
	if err != nil {
		fail("%v", err)
	}

	c := ensureServer()
	defer func() { _ = c.Close() }()

	target := float64(pct) / 100
	if relative {
		st, err := c.Status()
		if err != nil {
			fail("%v", err)
		}
		target += st.Volume
	}
	// The server clamps to [0, 1], so relative adjustments can't overshoot.
	st, err := c.SetVolume(target)
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(st)
		return
	}
	fmt.Printf("Volume:  %d%%\n", volumePercent(st.Volume))
}

// runVolumeMute toggles mute, restoring the pre-mute level (or a sensible
// default) on the way back.
func runVolumeMute(jsonOut bool) {
	c := ensureServer()
	defer func() { _ = c.Close() }()

	st, err := c.ToggleMute()
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(st)
		return
	}
	if st.Volume == 0 {
		fmt.Println("Muted")
	} else {
		fmt.Printf("Volume:  %d%%\n", volumePercent(st.Volume))
	}
}

// parseVolumeArg parses a volume argument: an absolute percentage in [0, 100],
// or a relative adjustment when explicitly signed ("+5", "-10").
func parseVolumeArg(arg string) (pct int, relative bool, err error) {
	relative = strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-")
	pct, convErr := strconv.Atoi(arg)
	if convErr != nil || (!relative && (pct < 0 || pct > 100)) {
		return 0, false, fmt.Errorf("volume must be a number between 0 and 100, or a +/- adjustment")
	}
	return pct, relative, nil
}

// showVolume prints the current volume without spawning a server: with no
// server running, the persisted state has the volume the next one will use.
func showVolume(jsonOut bool) {
	if c, _, running := dialServer(); running {
		defer func() { _ = c.Close() }()
		st, err := c.Status()
		if err != nil {
			fail("%v", err)
		}
		if jsonOut {
			printJSON(st)
			return
		}
		fmt.Printf("Volume:  %d%%\n", volumePercent(st.Volume))
		return
	}
	st, err := state.LoadState()
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(protocol.PlaybackState{Status: protocol.StatusStopped, Volume: st.GetVolume()})
		return
	}
	fmt.Printf("Volume:  %d%%\n", volumePercent(st.GetVolume()))
}

// historyDefaultLimit is how many entries `soma history` prints when -n is
// not given, matching the TUI's history overlay.
const historyDefaultLimit = 20

// runHistory prints recent now-playing titles: time, channel, and title,
// newest first. With a channel argument it filters to that channel (which
// may let the server backfill from SomaFM's own song history); -n bounds how
// many entries are shown. With --json, it prints the
// []protocol.HistoryEntry result instead of the human-readable table.
func runHistory(args []string) {
	usage := "soma history [--json] [-n N] [channel-id-or-name]"
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	fs.Usage = func() { _, _ = fmt.Fprintf(fs.Output(), "usage: %s\n", usage) }
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	n := fs.Int("n", historyDefaultLimit, "maximum number of entries to show")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) > 1 {
		fail("usage: %s", usage)
	}

	c := ensureServer()
	defer func() { _ = c.Close() }()

	var channelID string
	if len(rest) == 1 {
		payload := waitForCatalog(c)
		ch, err := resolveChannel(payload.Channels, rest[0])
		if err != nil {
			fail("%v", err)
		}
		channelID = ch.ID
	}

	entries, err := c.History(channelID, *n)
	if err != nil {
		fail("%v", err)
	}
	if *jsonOut {
		printJSON(entries)
		return
	}
	fmt.Print(formatHistory(entries))
}

// formatHistory renders history entries as one line per entry: local time,
// channel, and title.
func formatHistory(entries []protocol.HistoryEntry) string {
	if len(entries) == 0 {
		return "No history yet.\n"
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, e := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Time.Local().Format("2006-01-02 15:04"), e.ChannelTitle, e.Title)
	}
	_ = w.Flush()
	return b.String()
}

func runServerStop() {
	c, _, running := dialServer()
	if !running {
		fmt.Println("soma: server not running")
		return
	}
	defer func() { _ = c.Close() }()
	if err := c.Shutdown(); err != nil {
		fail("%v", err)
	}
	fmt.Println("soma: server stopped")
}
