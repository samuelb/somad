package main

import (
	"flag"
	"regexp"
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

// branchPattern matches a case-branch pattern line of the completion
// scripts' per-command dispatch, such as "list | status)".
var branchPattern = regexp.MustCompile(`^[a-z]+(\s*\|\s*[a-z]+)*\)$`)

// completionBranches splits the case statement that follows caseHeader in a
// completion script into branch bodies, keyed by every command name a
// branch's pattern lists.
func completionBranches(t *testing.T, script, caseHeader string) map[string]string {
	t.Helper()
	start := strings.Index(script, caseHeader)
	require.GreaterOrEqual(t, start, 0, "missing %q", caseHeader)

	branches := map[string]string{}
	var names []string
	var body strings.Builder
	for _, line := range strings.Split(script[start+len(caseHeader):], "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case names == nil && trimmed == "esac":
			return branches
		case names == nil && branchPattern.MatchString(trimmed):
			names = strings.Split(strings.TrimSuffix(trimmed, ")"), "|")
			body.Reset()
		case names != nil && trimmed == ";;":
			for _, n := range names {
				branches[strings.TrimSpace(n)] = body.String()
			}
			names = nil
		case names != nil:
			body.WriteString(line + "\n")
		}
	}
	t.Fatalf("no esac closes %q", caseHeader)
	return nil
}

// TestCompletionScriptsCoverCLI guards the hand-written scripts against
// drifting from the CLI: every command must be offered, every global flag
// too, and each command's own flags and arguments must be completed in that
// command's branch of the script — a flag that appears only under some other
// command does not count.
func TestCompletionScriptsCoverCLI(t *testing.T) {
	var globalFlags []string
	fs := flag.NewFlagSet("soma", flag.ContinueOnError)
	var cf connFlags
	cf.register(fs)
	fs.VisitAll(func(f *flag.Flag) { globalFlags = append(globalFlags, "--"+f.Name) })
	globalFlags = append(globalFlags, "--shutdown-on-exit")

	jsonOnly := []string{"--json"}
	perCommand := map[string][]string{
		"play":     jsonOnly,
		"list":     jsonOnly,
		"favorite": jsonOnly,
		"fav":      jsonOnly,
		"next":     jsonOnly,
		"prev":     jsonOnly,
		"pause":    jsonOnly,
		"stop":     {"--json", "--in", "--cancel"},
		"status":   jsonOnly,
		"volume":   {"--json", "mute"},
		"history":  {"--json", "-n"},
		// --json belongs to `soma lastfm status`.
		"lastfm": {"login", "logout", "status", "--json"},
		"daemon": {
			"stop", "--idle-timeout", "--no-tray", "--notify", "--quality", "--listen", "--tls",
			"--tls-cert", "--tls-key", "--psk-file", "--gen-psk", "--insecure", "--show-cert",
		},
		"completion": {"bash", "zsh"},
	}
	channelArgs := []string{"play", "favorite", "fav", "history"}

	for _, sh := range []struct {
		name, script, caseHeader, channelCompleter string
	}{
		{"bash", bashCompletion, `case "$cmd" in`, "soma completion channels"},
		{"zsh", zshCompletion, "case $words[1] in", "_soma_channels"},
	} {
		for _, want := range globalFlags {
			assert.Contains(t, sh.script, want, "%s completion is missing the global flag %s", sh.name, want)
		}
		branches := completionBranches(t, sh.script, sh.caseHeader)
		for cmd, wants := range perCommand {
			assert.Contains(t, sh.script, cmd, "%s completion does not offer the %s command", sh.name, cmd)
			branch, ok := branches[cmd]
			if !assert.True(t, ok, "%s completion has no branch for %s", sh.name, cmd) {
				continue
			}
			for _, want := range wants {
				assert.Contains(t, branch, want, "%s completion of soma %s is missing %q", sh.name, cmd, want)
			}
		}
		for _, cmd := range channelArgs {
			assert.Contains(t, branches[cmd], sh.channelCompleter, "%s completion of soma %s must complete channels", sh.name, cmd)
		}
		assert.Contains(t, sh.script, "soma completion channels", "%s completion must complete channels from the cache helper", sh.name)
	}
	assert.Contains(t, bashCompletion, "complete -F _soma soma")
	assert.True(t, strings.HasPrefix(zshCompletion, "#compdef soma"))
}
