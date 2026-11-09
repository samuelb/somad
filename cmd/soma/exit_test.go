package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mainArgsEnv, when set, makes the test binary run main with these
// arguments (separated by \x1f) instead of its tests, so a test can see the
// exit status a command line ends with.
const mainArgsEnv = "SOMA_TEST_MAIN_ARGS"

// mainVersionEnv, when set along with mainArgsEnv, is the version that run
// of main reports, for the commands that treat a daemon of another version
// differently: the test binary is otherwise "dev", which never counts as
// skewed.
const mainVersionEnv = "SOMA_TEST_MAIN_VERSION"

func TestMain(m *testing.M) {
	// A client that finds no daemon spawns one by running its own executable
	// as `<exe> daemon`, which here is this test binary: it would run every
	// test again, each of which may spawn in turn. No test means to.
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		fmt.Fprintln(os.Stderr, "the test binary was spawned as a soma daemon")
		os.Exit(3)
	}
	if args, ok := os.LookupEnv(mainArgsEnv); ok {
		if v, ok := os.LookupEnv(mainVersionEnv); ok {
			version = v
		}
		os.Args = append([]string{"soma"}, strings.Split(args, "\x1f")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runSoma runs the test binary as soma with args, its config, state, cache
// and socket confined to dir, and returns its combined output and exit
// status. env entries ("KEY=value") are added last, so they win.
func runSoma(t *testing.T, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0]) // #nosec G204 G702 -- the test binary itself
	cmd.Env = append(os.Environ(),
		mainArgsEnv+"="+strings.Join(args, "\x1f"),
		"XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir, "XDG_CACHE_HOME="+dir,
		"SOMAD_SOCKET="+dir+"/s.sock", "SOMAD_SERVER=",
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	status := 0
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
		status = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return string(out), status
}

// TestExitStatus pins the scripting contract: 0 for success, 1 for a
// command that ran and failed, 2 for a command line soma cannot make sense
// of. None of the commands here may spawn a daemon: that would run this
// test binary as one.
func TestExitStatus(t *testing.T) {
	dir := shortTempDir(t) // short: a socket path has a length limit

	tests := []struct {
		args []string
		want int
	}{
		{args: []string{"history", "--json"}, want: 0}, // no daemon: an empty history
		{args: []string{"--server", "127.0.0.1:1", "list"}, want: 1},
		{args: []string{"play", "a", "b"}, want: 2},
		{args: []string{"history", "-n", "0"}, want: 2},
		{args: []string{"history", "a", "b", "--json"}, want: 2},
		{args: []string{"stop", "--in", ""}, want: 2},
		{args: []string{"stop", "--in", "5m", "--cancel"}, want: 2},
		{args: []string{"volume", "loud"}, want: 2},
		{args: []string{"list", "--bogus"}, want: 2},
		{args: []string{"completion", "fish"}, want: 2},
		{args: []string{"bogus"}, want: 2},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			out, status := runSoma(t, dir, nil, tt.args...)
			assert.Equal(t, tt.want, status, "output: %s", out)
		})
	}
}
