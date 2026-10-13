package main

import (
	"errors"
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

func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(mainArgsEnv); ok {
		os.Args = append([]string{"soma"}, strings.Split(args, "\x1f")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestExitStatus pins the scripting contract: 0 for success, 1 for a
// command that ran and failed, 2 for a command line soma cannot make sense
// of. None of the commands here may spawn a daemon: that would run this
// test binary as one.
func TestExitStatus(t *testing.T) {
	dir, err := os.MkdirTemp("", "sx") // short: a socket path has a length limit
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

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
			cmd := exec.CommandContext(t.Context(), os.Args[0]) // #nosec G204 G702 -- the test binary itself
			cmd.Env = append(os.Environ(),
				mainArgsEnv+"="+strings.Join(tt.args, "\x1f"),
				"XDG_CONFIG_HOME="+dir, "XDG_STATE_HOME="+dir, "XDG_CACHE_HOME="+dir,
				"SOMAD_SOCKET="+dir+"/s.sock", "SOMAD_SERVER=",
			)
			out, err := cmd.CombinedOutput()
			status := 0
			if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
				status = exitErr.ExitCode()
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, status, "output: %s", out)
		})
	}
}
