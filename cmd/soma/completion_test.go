package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"somad/internal/channels"
	"somad/internal/config"

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
		"previous": jsonOnly,
		"pause":    jsonOnly,
		"stop":     {"--json", "--in", "--cancel"},
		"status":   jsonOnly,
		"volume":   {"--json", "mute"},
		"history":  {"--json", "-n"},
		// --json belongs to `soma lastfm status`.
		"lastfm":     {"login", "logout", "status", "--json"},
		"daemon":     {"stop"},
		"completion": {"bash", "zsh"},
	}
	newDaemonFlagSet(&config.Config{}, &daemonFlags{}).VisitAll(func(f *flag.Flag) {
		perCommand["daemon"] = append(perCommand["daemon"], "--"+f.Name)
	})
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

// completionSandbox is where the completion scripts run in the tests below:
// a directory with nothing of the user's in it, and an environment whose
// PATH leads with a stand-in `soma`. The scripts call soma for one thing,
// `soma completion channels`, and the stand-in answers that, and only
// that, with what the real helper prints for testCatalog. So completing in
// these tests cannot reach a daemon or the user's cache; should a script
// ever run something else, every soma path points into the sandbox too.
type completionSandbox struct {
	dir string
	env []string
}

func newCompletionSandbox(t *testing.T) completionSandbox {
	t.Helper()
	dir := shortTempDir(t)
	t.Setenv("XDG_CACHE_HOME", dir)
	require.NoError(t, channels.WriteChannelsToCache(&channels.Channels{Channels: testCatalog}))
	var helperOutput strings.Builder
	printChannelCompletions(&helperOutput)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "channels"), []byte(helperOutput.String()), 0o600))

	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, 0o750))
	stub := "#!/bin/sh\n" +
		"if [ \"$*\" = \"completion channels\" ]; then\n" +
		"    exec cat \"$SOMA_TEST_CHANNELS\"\n" +
		"fi\n" +
		"echo \"unexpected: soma $*\" >&2\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "soma"), []byte(stub), 0o700)) // #nosec G306 -- an executable stub
	return completionSandbox{dir: dir, env: []string{
		"PATH=" + bin + ":/usr/bin:/bin",
		"SOMA_TEST_CHANNELS=" + filepath.Join(dir, "channels"),
		"HOME=" + dir, "ZDOTDIR=" + dir, "TERM=dumb",
		"XDG_CONFIG_HOME=" + dir, "XDG_STATE_HOME=" + dir, "XDG_CACHE_HOME=" + dir,
		"SOMAD_SOCKET=" + dir + "/s.sock", "SOMAD_SERVER=",
	}}
}

// bashShells returns every distinct bash on this machine: the one on PATH,
// and the usual homes of a current one next to macOS's own 3.2, since the
// script has to work on both.
func bashShells() []string {
	candidates := []string{"/bin/bash", "/opt/homebrew/bin/bash", "/usr/local/bin/bash"}
	if onPath, err := exec.LookPath("bash"); err == nil {
		candidates = append(candidates, onPath)
	}
	var shells []string
	seen := map[string]bool{}
	for _, c := range candidates {
		resolved, err := filepath.EvalSymlinks(c)
		if err != nil || seen[resolved] {
			continue
		}
		seen[resolved] = true
		shells = append(shells, c)
	}
	return shells
}

// bashCaseEnd ends each command line in bashDriver's arguments.
const bashCaseEnd = "@@"

// bashDriver sources the completion script it is given, then takes command
// lines, each already split into the words bash would hand a completion
// function and ended by bashCaseEnd. It completes the last word of each
// and prints one line of candidates per command line. All of them go
// through one bash, since starting a process is slow under the race
// detector.
const bashDriver = `source "$1"
shift
COMP_WORDS=()
for arg in "$@"; do
    if [[ "$arg" != "` + bashCaseEnd + `" ]]; then
        COMP_WORDS+=("$arg")
        continue
    fi
    COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
    COMP_LINE="${COMP_WORDS[*]}"
    COMP_POINT=${#COMP_LINE}
    COMPREPLY=()
    _soma
    echo "${COMPREPLY[*]}"
    COMP_WORDS=()
done
`

// TestBashCompletion runs the bash script. Which words reach it depends on
// the bash: 4 and later also break the line at "=" and ":"
// (COMP_WORDBREAKS), 3.2 only at blanks. The words are given here as either
// would split them, so both splits are tested on whatever bash there is.
func TestBashCompletion(t *testing.T) {
	shells := bashShells()
	if len(shells) == 0 {
		t.Skip("no bash found")
	}
	sb := newCompletionSandbox(t)
	script := filepath.Join(sb.dir, "soma.bash")
	driver := filepath.Join(sb.dir, "driver.bash")
	require.NoError(t, os.WriteFile(script, []byte(bashCompletion), 0o600))
	require.NoError(t, os.WriteFile(driver, []byte(bashDriver), 0o600))

	commands := []string{"play", "list", "favorite", "next", "prev", "pause", "stop", "status", "volume",
		"history", "lastfm", "daemon", "completion", "help", "version"}
	tests := []struct {
		name  string
		words []string // the line as bash splits it; the last word is completed
		want  []string
	}{
		{name: "commands", words: []string{"soma", ""}, want: commands},
		{name: "command prefix", words: []string{"soma", "pl"}, want: []string{"play"}},
		{name: "global flags", words: []string{"soma", "--s"}, want: []string{"--server", "--shutdown-on-exit"}},
		{name: "after a boolean flag", words: []string{"soma", "--tls", "pl"}, want: []string{"play"}},

		// A value holding a colon, as bash 4 and later split it.
		{name: "after host:port", words: []string{"soma", "--server", "host", ":", "5454", "pl"}, want: []string{"play"}},
		{name: "after =host:port", words: []string{"soma", "--server", "=", "host", ":", "5454", "pl"}, want: []string{"play"}},
		{name: "after an IPv6 address", words: []string{"soma", "--server", "[", "::", "1]", ":", "5454", "st"}, want: []string{"stop", "status"}},
		{name: "after a fingerprint", words: []string{"soma", "--tls-fingerprint", "sha256", ":", "abcd", "pl"}, want: []string{"play"}},
		{name: "after =fingerprint", words: []string{"soma", "--tls-fingerprint", "=", "sha256", ":", "abcd", "pl"}, want: []string{"play"}},
		{name: "channel after host:port", words: []string{"soma", "--server", "host", ":", "5454", "play", "gro"}, want: []string{"groovesalad"}},
		{name: "command flag after host:port", words: []string{"soma", "--server", "host", ":", "5454", "stop", "--"}, want: []string{"--json", "--in", "--cancel"}},
		{name: "lastfm action after host:port", words: []string{"soma", "--server", "host", ":", "5454", "lastfm", "lo"}, want: []string{"login", "logout"}},
		{name: "lastfm status flag after host:port", words: []string{"soma", "--server", "host", ":", "5454", "lastfm", "status", "--"}, want: []string{"--json"}},
		{name: "lastfm login takes no flag", words: []string{"soma", "--server", "host", ":", "5454", "lastfm", "login", "--"}},
		// The same, as bash 3.2 leaves it.
		{name: "after host:port in one word", words: []string{"soma", "--server", "host:5454", "pl"}, want: []string{"play"}},
		{name: "after --server=host:port in one word", words: []string{"soma", "--server=host:5454", "pl"}, want: []string{"play"}},
		{name: "channel after host:port in one word", words: []string{"soma", "--server", "host:5454", "play", "gro"}, want: []string{"groovesalad"}},

		// The cursor inside a flag's value: no command belongs there.
		{name: "value expected", words: []string{"soma", "--server", ""}},
		{name: "inside host:port", words: []string{"soma", "--server", "host", ":", "54"}},
		{name: "right after the colon", words: []string{"soma", "--server", "host", ":"}},
		{name: "inside =host", words: []string{"soma", "--server", "=", "pl"}},

		{name: "channels", words: []string{"soma", "play", "drone"}, want: []string{"dronezone", "dronezonedeep"}},
		{name: "favorite alias", words: []string{"soma", "fav", "sec"}, want: []string{"secretagent"}},
		{name: "history channel", words: []string{"soma", "history", "gro"}, want: []string{"groovesalad"}},
		{name: "history flags", words: []string{"soma", "history", "-"}, want: []string{"--json", "-n"}},
		{name: "prev", words: []string{"soma", "prev", "--j"}, want: []string{"--json"}},
		{name: "previous alias", words: []string{"soma", "previous", "--j"}, want: []string{"--json"}},
		{name: "volume", words: []string{"soma", "volume", "m"}, want: []string{"mute"}},
		{name: "completion shells", words: []string{"soma", "completion", ""}, want: []string{"bash", "zsh"}},
		{name: "daemon flag after a listen address", words: []string{"soma", "daemon", "--listen", "0.0.0.0", ":", "5454", "--tls-c"}, want: []string{"--tls-cert"}},
		{name: "daemon quality", words: []string{"soma", "daemon", "--quality", "hi"}, want: []string{"highest", "high"}},
		{name: "daemon =quality", words: []string{"soma", "daemon", "--quality", "=", "l"}, want: []string{"low"}},
	}
	args := []string{"--noprofile", "--norc", driver, script}
	for _, tt := range tests {
		args = append(append(args, tt.words...), bashCaseEnd)
	}
	for _, bash := range shells {
		t.Run(bash, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), bash, args...) // #nosec G204 -- a bash found on this machine, running the test's own script
			cmd.Env = sb.env
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "output: %s", out)
			replies := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
			require.Len(t, replies, len(tests), "one line of candidates per command line; output: %s", out)
			for i, tt := range tests {
				assert.ElementsMatch(t, tt.want, strings.Fields(replies[i]), "%s: completing %q", tt.name, tt.words)
			}
		})
	}
}

// TestZshCompletion runs the zsh script: testdata/zsh-complete.zsh presses
// Tab at the end of each line in a zsh on a pty of its own and prints what
// is offered. It is skipped where that cannot be done (no zsh, or one
// without the zpty module). Like the bash test, it goes through one process
// for all lines.
func TestZshCompletion(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("no zsh found")
	}
	sb := newCompletionSandbox(t)
	fdir := filepath.Join(sb.dir, "fpath")
	require.NoError(t, os.Mkdir(fdir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(fdir, "_soma"), []byte(zshCompletion), 0o600))
	harness, err := filepath.Abs("testdata/zsh-complete.zsh")
	require.NoError(t, err)

	tests := []struct {
		name string
		line string
		want []string
	}{
		{name: "command prefix", line: "soma pl", want: []string{"play"}},
		{name: "after host:port", line: "soma --server host:5454 pl", want: []string{"play"}},
		{name: "after =host:port", line: "soma --server=host:5454 pl", want: []string{"play"}},
		{name: "after =fingerprint", line: "soma --tls-fingerprint=sha256:abcd st", want: []string{"stop", "status"}},
		{name: "channel after =host:port", line: "soma --server=host:5454 play gro", want: []string{"groovesalad"}},
		{name: "channels", line: "soma favorite drone", want: []string{"dronezone", "dronezonedeep"}},
		{name: "prev", line: "soma prev --j", want: []string{"--json"}},
		{name: "previous alias", line: "soma previous --j", want: []string{"--json"}},
		{name: "stop flags", line: "soma stop --", want: []string{"--json", "--in", "--cancel"}},
		{name: "daemon =quality", line: "soma daemon --quality=l", want: []string{"low"}},
		{name: "lastfm status flag", line: "soma lastfm status --j", want: []string{"--json"}},
	}
	args := make([]string, 0, 4+len(tests))
	args = append(args, "-f", harness, fdir, sb.dir)
	for _, tt := range tests {
		args = append(args, tt.line)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, zsh, args...) // #nosec G204 -- the zsh on PATH, running the test's own script
	cmd.Env = sb.env
	out, err := cmd.Output()
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) && exitErr.ExitCode() == 3 {
		t.Skip("this zsh cannot drive completion on a pty (no zpty module)")
	}
	require.NoError(t, err, "output: %s", out)

	// "line <n>" opens each command line's candidates, "match <candidate>"
	// is one of them. A candidate can come up more than once, as zsh asks
	// a completer again for each kind of match it tries.
	got := make([][]string, len(tests))
	current := -1
	for _, l := range strings.Split(string(out), "\n") {
		if n, ok := strings.CutPrefix(l, "line "); ok {
			current, err = strconv.Atoi(n)
			require.NoError(t, err, "output: %s", out)
			require.Less(t, current, len(tests), "output: %s", out)
		} else if match, ok := strings.CutPrefix(l, "match "); ok && current >= 0 {
			if !slices.Contains(got[current], match) {
				got[current] = append(got[current], match)
			}
		}
	}
	require.Equal(t, len(tests)-1, current, "every command line must have been completed; output: %s", out)
	for i, tt := range tests {
		assert.ElementsMatch(t, tt.want, got[i], "%s: completing %q", tt.name, tt.line)
	}
}

// TestBashCompletionPassesShellcheck lints the bash script where shellcheck
// is installed.
func TestBashCompletionPassesShellcheck(t *testing.T) {
	shellcheck, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Skip("no shellcheck found")
	}
	out, err := exec.CommandContext(t.Context(), shellcheck, "-s", "bash", "completions/soma.bash").CombinedOutput() // #nosec G204 -- the shellcheck on PATH
	require.NoError(t, err, "output: %s", out)
}

// TestCompletionScriptsParse has each shell check its script's syntax, for
// a machine where the tests above could not run it.
func TestCompletionScriptsParse(t *testing.T) {
	for _, sh := range []struct{ shell, script string }{
		{"bash", bashCompletion},
		{"zsh", zshCompletion},
	} {
		t.Run(sh.shell, func(t *testing.T) {
			path, err := exec.LookPath(sh.shell)
			if err != nil {
				t.Skipf("no %s found", sh.shell)
			}
			file := filepath.Join(t.TempDir(), "soma."+sh.shell)
			require.NoError(t, os.WriteFile(file, []byte(sh.script), 0o600))
			out, err := exec.CommandContext(t.Context(), path, "-n", file).CombinedOutput() // #nosec G204 -- the shell on PATH, parsing the test's own file
			require.NoError(t, err, "output: %s", out)
		})
	}
}
