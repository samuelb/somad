package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"somatui/internal/channels"
	"somatui/internal/client"
	"somatui/internal/protocol"
)

// catalogWait bounds how long CLI commands wait for a freshly spawned
// server to finish loading the channel catalog.
const catalogWait = 15 * time.Second

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "somatui: "+format+"\n", args...)
	os.Exit(1)
}

// ensureServer connects (spawning the server if needed) and warns about a
// version mismatch that could not be auto-healed.
func ensureServer() *client.Client {
	c, hr, err := client.EnsureServer(protocol.SocketPath(), version)
	if err != nil {
		fail("%v", err)
	}
	if hr.ServerVersion != version {
		fmt.Fprintf(os.Stderr,
			"somatui: warning: server runs v%s, client v%s — restart it with `somatui server stop` once playback is stopped\n",
			hr.ServerVersion, version)
	}
	return c
}

// dialServer connects to a running server without spawning one. The second
// return value is false when no server is listening.
func dialServer() (*client.Client, bool) {
	c, err := client.Dial(protocol.SocketPath())
	if err != nil {
		return nil, false
	}
	if _, err := c.Hello(version); err != nil {
		_ = c.Close()
		fail("%v", err)
	}
	return c, true
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
		fail("usage: somatui play [channel-id-or-name]")
	}
	c := ensureServer()
	defer func() { _ = c.Close() }()

	payload := waitForCatalog(c)
	var ch channels.Channel
	if len(args) == 0 {
		// Without an argument, resume the last played channel.
		if payload.LastChannelID == "" {
			fail("no previously played channel; usage: somatui play <channel-id-or-name>")
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
	c := ensureServer()
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
	c, running := dialServer()
	if !running {
		fmt.Println("somatui: not playing (server not running)")
		return
	}
	defer func() { _ = c.Close() }()

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
	c, running := dialServer()
	if !running {
		fmt.Println("somatui: not playing (server not running)")
		return
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Stop(); err != nil {
		fail("%v", err)
	}
	fmt.Println("Stopped")
}

func runStatus() {
	c, running := dialServer()
	if !running {
		fmt.Println("somatui: stopped (server not running)")
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
	fmt.Printf("Volume:  %d%%\n", int(st.Volume*100+0.5))
}

func runVolume(args []string) {
	if len(args) != 1 {
		fail("usage: somatui volume <0-100>")
	}
	pct, err := strconv.Atoi(args[0])
	if err != nil || pct < 0 || pct > 100 {
		fail("volume must be a number between 0 and 100")
	}
	c := ensureServer()
	defer func() { _ = c.Close() }()

	st, err := c.SetVolume(float64(pct) / 100)
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("Volume:  %d%%\n", int(st.Volume*100+0.5))
}

func runServerStop() {
	c, running := dialServer()
	if !running {
		fmt.Println("somatui: server not running")
		return
	}
	defer func() { _ = c.Close() }()
	if err := c.Shutdown(); err != nil {
		fail("%v", err)
	}
	fmt.Println("somatui: server stopped")
}
