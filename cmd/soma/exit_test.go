package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"somad/internal/audio"
	"somad/internal/channels"
	"somad/internal/security"

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
		// A daemon run this way must not reach for SomaFM: outbound requests
		// to a host that is not on the allowlist fail before any network
		// I/O, and the daemon runs on without a catalog.
		channels.SomaFMChannelsURL = "http://catalog.invalid.test/channels.json"
		if marker := os.Getenv(lostDeviceEnv); marker != "" {
			newPlayer = func(string) audio.Player { return &lostOncePlayer{marker: marker} }
			// The playlist of the one channel in its catalog is served by
			// the test that started it.
			security.AddAllowedHost("127.0.0.1")
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
	t.Parallel() // each case is a process of its own in a directory of its own
	const fingerprint = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		args    []string
		env     []string // added to the command's environment
		config  string   // config.yaml content, when the case needs one
		want    int
		wantOut string // when set, the output must contain it
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

		// Asking for help is not a mistake, whoever parses the arguments.
		{args: []string{"play", "--help"}, want: 0, wantOut: "usage: soma play"},
		{args: []string{"volume", "--help"}, want: 0, wantOut: "usage: soma volume"},
		{args: []string{"volume", "-h"}, want: 0, wantOut: "usage: soma volume"},
		{args: []string{"volume", "--json", "--help"}, want: 0, wantOut: "usage: soma volume"},
		{args: []string{"daemon", "--help"}, want: 0, wantOut: "Usage: soma daemon [flags]"},

		// Connection flags that are wrong in themselves...
		{args: []string{"--tls", "status"}, want: 2, wantOut: "require --server"},
		{args: []string{"--psk-file", "/nonexistent/psk", "status"}, want: 2, wantOut: "require --server"},
		{args: []string{"--server", "nohostport", "list"}, want: 2, wantOut: "invalid server address"},
		{args: []string{"--server", "127.0.0.1:1", "--tls-fingerprint", "nothex", "list"}, want: 2, wantOut: "invalid TLS fingerprint"},
		{args: []string{"--server", "127.0.0.1:1", "--tls-ca", "ca.pem", "--tls-fingerprint", fingerprint, "list"}, want: 2, wantOut: "mutually exclusive"},
		// ...as opposed to the environment or the config file being wrong,
		// or a file the flags name.
		{args: []string{"list"}, env: []string{"SOMAD_SERVER=nohostport"}, want: 1, wantOut: "invalid server address"},
		{args: []string{"list"}, config: "client:\n  server: nohostport\n", want: 1, wantOut: "invalid server address"},
		{args: []string{"list"}, config: "client:\n  bogus: 1\n", want: 1, wantOut: "error loading config"},
		{args: []string{"--server", "127.0.0.1:1", "--psk-file", "/nonexistent/psk", "list"}, want: 1, wantOut: "opening PSK file"},
		{args: []string{"--server", "127.0.0.1:1", "--tls-ca", "/nonexistent/ca.pem", "list"}, want: 1, wantOut: "reading TLS CA file"},

		// --shutdown-on-exit belongs to the TUI.
		{args: []string{"--shutdown-on-exit", "status"}, want: 2, wantOut: "--shutdown-on-exit only applies to the TUI"},
		{args: []string{"--shutdown-on-exit", "daemon", "stop"}, want: 2, wantOut: "--shutdown-on-exit only applies to the TUI"},
		{args: []string{"--shutdown-on-exit", "daemon"}, want: 2, wantOut: "--shutdown-on-exit does not apply to the daemon"},

		// The daemon's own command line. None of these gets as far as
		// starting one.
		{args: []string{"--tls", "daemon"}, want: 2, wantOut: "--tls does not apply to the daemon"},
		{args: []string{"daemon", "bogus"}, want: 2, wantOut: `unexpected argument "bogus"`},
		{args: []string{"daemon", "--no-tray", "stop"}, want: 2, wantOut: `unexpected argument "stop"`},
		{args: []string{"daemon", "--bogus"}, want: 2},
		{args: []string{"daemon", "--quality", "nope"}, want: 2, wantOut: "--quality"},
		{args: []string{"daemon", "--idle-timeout", "-5s"}, want: 2, wantOut: "--idle-timeout must not be negative"},
		{args: []string{"daemon", "--idle-timeout", "soon"}, want: 2},
		{args: []string{"daemon", "--tls-cert", "/x"}, want: 2, wantOut: "must be set together"},
		{args: []string{"daemon", "--psk-file", "/nonexistent/psk"}, want: 1, wantOut: "error reading the PSK file"},
		{args: []string{"daemon", "--show-cert", "--tls-cert", "/nonexistent/c.pem", "--tls-key", "/nonexistent/k.pem"}, want: 1, wantOut: "error loading the TLS certificate"},
		{args: []string{"daemon"}, config: "server:\n  quality: nope\n", want: 1, wantOut: "error loading config"},
	}
	for _, tt := range tests {
		name := strings.Join(tt.args, " ")
		if tt.env != nil || tt.config != "" {
			name += " (" + strings.Join(append(tt.env, strings.Fields(tt.config)...), " ") + ")"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := shortTempDir(t) // short: a socket path has a length limit
			configFile := filepath.Join(dir, "somad", "config.yaml")
			if tt.config != "" {
				require.NoError(t, os.Mkdir(filepath.Dir(configFile), 0o750))
				require.NoError(t, os.WriteFile(configFile, []byte(tt.config), 0o600))
			}

			out, status := runSoma(t, dir, tt.env, tt.args...)

			assert.Equal(t, tt.want, status, "output: %s", out)
			if tt.wantOut != "" {
				assert.Contains(t, out, tt.wantOut)
			}
			if tt.config == "" && tt.want != 1 {
				// A command line soma could not parse, or one that only
				// asked for help, must not leave the daemon's config
				// template behind.
				assert.NoFileExists(t, configFile)
			}
		})
	}
}
