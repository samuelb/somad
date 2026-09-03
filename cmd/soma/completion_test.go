package main

import (
	"strings"
	"testing"

	"somad/internal/channels"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintChannelCompletions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	require.NoError(t, channels.WriteChannelsToCache(&channels.Channels{Channels: testCatalog}))

	var b strings.Builder
	printChannelCompletions(&b)

	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	assert.Len(t, lines, len(testCatalog))
	assert.Contains(t, lines, "dronezone\tDrone Zone")
	assert.Contains(t, lines, "groovesalad\tGroove Salad")
}

func TestPrintChannelCompletions_NoCache(t *testing.T) {
	// Without a cache the helper completes nothing; it must not spawn a
	// server or hit the network to get one.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var b strings.Builder
	printChannelCompletions(&b)
	assert.Empty(t, b.String())
}

// TestCompletionScriptsCoverCLI guards the hand-written scripts against
// drifting from the CLI: every command and flag soma accepts must appear in
// both scripts (and the scripts' channel helper must stay wired up).
func TestCompletionScriptsCoverCLI(t *testing.T) {
	commands := []string{
		"play", "list", "favorite", "next", "prev", "pause", "stop",
		"status", "volume", "daemon", "completion",
	}
	flags := []string{
		// global connection/TUI flags
		"--server", "--tls", "--tls-ca", "--tls-fingerprint", "--psk-file",
		"--shutdown-on-exit",
		// daemon flags
		"--idle-timeout", "--no-tray", "--quality", "--listen", "--tls-cert", "--tls-key",
		"--gen-psk", "--show-cert",
		// per-command output flag
		"--json",
	}
	for name, script := range map[string]string{"bash": bashCompletion, "zsh": zshCompletion} {
		for _, want := range append(commands, flags...) {
			assert.Contains(t, script, want, "%s completion is missing %q", name, want)
		}
		assert.Contains(t, script, "soma completion channels", "%s completion must complete channels from the cache helper", name)
	}
	assert.Contains(t, bashCompletion, "complete -F _soma soma")
	assert.True(t, strings.HasPrefix(zshCompletion, "#compdef soma"))
}
