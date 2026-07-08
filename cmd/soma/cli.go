package main

import (
	"encoding/json"
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
	c, _, err := client.EnsureServer(protocol.SocketPath(), version)
	if err != nil {
		fail("%v", err)
	}
	return c
}

// ensureServerForPlayback connects for a command that changes the stream. Since
// that interrupts playback anyway, an out-of-date server is restarted onto our
// version first, so the command runs against the up-to-date binary.
func ensureServerForPlayback() *client.Client {
	c, _, err := client.EnsureServerForPlayback(protocol.SocketPath(), version)
	if err != nil {
		fail("%v", err)
	}
	return c
}

// dialServer connects to a running server without spawning one, returning its
// reported version. The last return value is false when no server is listening.
func dialServer() (*client.Client, string, bool) {
	c, err := client.Dial(protocol.SocketPath())
	if err != nil {
		return nil, "", false
	}
	hr, err := c.Hello(version)
	if err != nil {
		_ = c.Close()
		fail("%v", err)
	}
	return c, hr.ServerVersion, true
}

// restartForUpgrade restarts an out-of-date server onto our version, for a
// command that is about to interrupt playback anyway. It returns c unchanged
// when the server already runs our version.
func restartForUpgrade(c *client.Client, serverVersion string) *client.Client {
	if serverVersion == version {
		return c
	}
	nc, _, err := client.Restart(c, protocol.SocketPath(), version)
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
	if len(args) > 1 {
		fail("usage: soma play [channel-id-or-name]")
	}
	c := ensureServerForPlayback()
	defer func() { _ = c.Close() }()

	payload := waitForCatalog(c)
	var ch channels.Channel
	if len(args) == 0 {
		// Without an argument, resume the last played channel.
		if payload.LastChannelID == "" {
			fail("no previously played channel; usage: soma play <channel-id-or-name>")
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
	fmt.Printf("Playing: %s\n", st.ChannelTitle)
}

// extractJSONFlag reports whether "--json" is present in args, returning the
// remaining arguments with it removed.
func extractJSONFlag(args []string) (rest []string, jsonOut bool) {
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, jsonOut
}

// runList prints the channel catalog, favorites first and marked with a
// star, one channel per line for browsing and scripting. With --json, it
// prints the same catalog as a JSON array for scripts to parse.
func runList(args []string) {
	args, jsonOut := extractJSONFlag(args)
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
	fav := make(map[string]bool, len(payload.Favorites))
	for _, id := range payload.Favorites {
		fav[id] = true
	}
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
	fav := make(map[string]bool, len(payload.Favorites))
	for _, id := range payload.Favorites {
		fav[id] = true
	}
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
	args, jsonOut := extractJSONFlag(args)
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
// the current or last played one, in catalog order (favorites first).
func runPlayRelative(delta int) {
	c := ensureServerForPlayback()
	defer func() { _ = c.Close() }()

	// A freshly spawned server may still be loading the catalog.
	waitForCatalog(c)

	st, err := c.PlayRelative(delta)
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("Playing: %s\n", st.ChannelTitle)
}

// runPause toggles between stopped and playing. Live radio has no real
// pause: unpausing reconnects to the live stream of the last channel.
func runPause() {
	c, serverVersion, running := dialServer()
	if !running {
		fmt.Println("soma: not playing (server not running)")
		return
	}
	defer func() { _ = c.Close() }()

	if serverVersion != version {
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
			fmt.Println("Paused")
			return
		}
	}

	st, err := c.PlayPause()
	if err != nil {
		fail("%v", err)
	}
	if st.Status == protocol.StatusStopped {
		fmt.Println("Paused")
	} else {
		fmt.Printf("Playing: %s\n", st.ChannelTitle)
	}
}

func runStop() {
	c, serverVersion, running := dialServer()
	if !running {
		fmt.Println("soma: not playing (server not running)")
		return
	}
	defer func() { _ = c.Close() }()
	// Stopping interrupts playback anyway, so upgrade an out-of-date server now;
	// the fresh server starts stopped, which is the state stop leaves us in.
	c = restartForUpgrade(c, serverVersion)
	if _, err := c.Stop(); err != nil {
		fail("%v", err)
	}
	fmt.Println("Stopped")
}

// runStatus prints the playback state, as JSON with --json so status bars
// and scripts don't have to parse the human-readable output.
func runStatus(args []string) {
	jsonOut := false
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--json":
		jsonOut = true
	default:
		fail("usage: soma status [--json]")
	}

	c, _, running := dialServer()
	if !running {
		if jsonOut {
			// No server means stopped; the persisted volume is what the next
			// server will use, so the snapshot is complete without one.
			st := protocol.PlaybackState{Status: protocol.StatusStopped}
			if s, err := state.LoadState(); err == nil {
				st.Volume = s.GetVolume()
			}
			printJSON(st)
			return
		}
		fmt.Println("soma: stopped (server not running)")
		return
	}
	defer func() { _ = c.Close() }()

	st, err := c.Status()
	if err != nil {
		fail("%v", err)
	}
	if jsonOut {
		printJSON(st)
		return
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
// absolute percentage, and adjusts it for an explicitly signed one.
func runVolume(args []string) {
	if len(args) == 0 {
		showVolume()
		return
	}
	if len(args) != 1 {
		fail("usage: soma volume [<0-100> | +<n> | -<n>]")
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
	fmt.Printf("Volume:  %d%%\n", volumePercent(st.Volume))
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
func showVolume() {
	if c, _, running := dialServer(); running {
		defer func() { _ = c.Close() }()
		st, err := c.Status()
		if err != nil {
			fail("%v", err)
		}
		fmt.Printf("Volume:  %d%%\n", volumePercent(st.Volume))
		return
	}
	st, err := state.LoadState()
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("Volume:  %d%%\n", volumePercent(st.GetVolume()))
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
