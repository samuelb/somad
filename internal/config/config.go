// Package config loads the optional user configuration file. It holds
// settings that mirror the server's CLI flags, so they also apply when the
// server is auto-spawned (which passes no flags); explicit flags take
// precedence over the file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	configFileName = "config.yaml"
	appDirName     = "somad"
)

// Config is the parsed configuration file. Fields are pointers so an
// explicit zero value ("tray: false", "idle_timeout: 0") is distinguishable
// from an absent key, which falls back to the built-in default.
type Config struct {
	Server ServerConfig `yaml:"server"`
	Client ClientConfig `yaml:"client"`
	TUI    TUIConfig    `yaml:"tui"`
}

// ServerConfig configures the playback server, mirroring the flags of
// `soma daemon`.
type ServerConfig struct {
	// IdleTimeout is how long the server lingers with no connected clients
	// and stopped playback before exiting; 0 disables the timeout.
	IdleTimeout *Duration `yaml:"idle_timeout"`
	// Tray controls the system tray / menu-bar icon (the inverse of the
	// --no-tray flag, so the file reads positively).
	Tray *bool `yaml:"tray"`
	// Listen is a host:port the server additionally listens on over TCP,
	// for frontends on other machines. Empty keeps the server local-only
	// (Unix socket).
	Listen *string `yaml:"listen"`
	// TLS enables TLS on the TCP listener. Without TLSCert/TLSKey a
	// self-signed certificate is generated (and reused) in the state dir.
	TLS *bool `yaml:"tls"`
	// TLSCert and TLSKey are PEM files for the TCP listener; setting them
	// implies TLS. They must be set together.
	TLSCert *string `yaml:"tls_cert"`
	TLSKey  *string `yaml:"tls_key"`
	// PSK is the pre-shared key TCP clients must authenticate with; PSKFile
	// reads it from a file instead, keeping the secret out of this file.
	// At most one may be set. The Unix socket never requires it.
	PSK     *string `yaml:"psk"`
	PSKFile *string `yaml:"psk_file"`
}

// ClientConfig configures how the TUI and CLI reach the server. It mirrors
// the global client flags (--server, --tls, --tls-ca, --tls-fingerprint,
// --psk-file); explicit flags take precedence.
type ClientConfig struct {
	// Server is the host:port of a remote soma daemon. Empty means the
	// local Unix socket (the default).
	Server *string `yaml:"server"`
	// TLS forces TLS on; it is implied by TLSCA or TLSFingerprint.
	TLS *bool `yaml:"tls"`
	// TLSCA is a PEM file with the certificate (or CA) to trust.
	TLSCA *string `yaml:"tls_ca"`
	// TLSFingerprint pins the server certificate by its SHA-256 fingerprint
	// ("sha256:<hex>", as printed by the server at startup). At most one of
	// TLSCA and TLSFingerprint may be set; with neither, the system trust
	// store is used.
	TLSFingerprint *string `yaml:"tls_fingerprint"`
	// PSK is the pre-shared key for the server; PSKFile reads it from a
	// file instead. At most one may be set.
	PSK     *string `yaml:"psk"`
	PSKFile *string `yaml:"psk_file"`
}

// TUIConfig configures the terminal frontend.
type TUIConfig struct {
	// ShutdownOnExit stops playback and shuts down the server when the TUI exits.
	ShutdownOnExit *bool `yaml:"shutdown_on_exit"`
}

// Duration wraps time.Duration so the YAML file can use Go duration syntax
// ("90s", "5m", "1h30m", "0").
type Duration time.Duration

// UnmarshalYAML parses a duration string; bare numbers are rejected so the
// unit is always explicit.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("line %d: durations must be strings like \"5m\" or \"90s\"", value.Line)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q (use Go syntax like \"5m\" or \"90s\")", value.Line, s)
	}
	*d = Duration(parsed)
	return nil
}

// Path returns the configuration file path without requiring it to exist.
// On Linux: $XDG_CONFIG_HOME/somad/config.yaml or ~/.config/somad/config.yaml
// On macOS: ~/Library/Application Support/somad/config.yaml
func Path() (string, error) {
	var baseDir string

	// Check XDG override first (works on all platforms, enables testing)
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		baseDir = xdgConfig
	} else if runtime.GOOS == "darwin" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, "Library", "Application Support")
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		baseDir = filepath.Join(homeDir, ".config")
	}

	return filepath.Join(baseDir, appDirName, configFileName), nil
}

// Load reads the configuration file. A missing file is not an error and
// yields the zero Config; a file that exists but does not parse is an error,
// because silently ignoring a hand-written config would be worse than
// refusing to start.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path derived from the user config dir, not user input
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	// Reject unknown keys so a typo ("idle_timout") fails loudly instead of
	// silently applying the default.
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) { // an empty file is a valid, empty config
			return &Config{}, nil
		}
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}

	if cfg.Server.IdleTimeout != nil && *cfg.Server.IdleTimeout < 0 {
		return nil, fmt.Errorf("invalid config file %s: server.idle_timeout must not be negative", path)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", path, err)
	}
	return &cfg, nil
}

// validate rejects contradictory remote-transport settings, so a
// misconfigured setup fails at startup instead of at connect time.
func (c *Config) validate() error {
	set := func(s *string) bool { return s != nil && *s != "" }
	if set(c.Server.TLSCert) != set(c.Server.TLSKey) {
		return errors.New("server.tls_cert and server.tls_key must be set together")
	}
	if set(c.Server.PSK) && set(c.Server.PSKFile) {
		return errors.New("server.psk and server.psk_file are mutually exclusive")
	}
	if set(c.Client.PSK) && set(c.Client.PSKFile) {
		return errors.New("client.psk and client.psk_file are mutually exclusive")
	}
	if set(c.Client.TLSCA) && set(c.Client.TLSFingerprint) {
		return errors.New("client.tls_ca and client.tls_fingerprint are mutually exclusive")
	}
	return nil
}

// templateFormat is the generated default config file. Every setting is
// commented out, so parsing it yields the built-in defaults even when those
// change in a later release.
const templateFormat = `# Soma configuration file.
#
# Generated with the built-in defaults, everything commented out; uncomment a
# setting to change it. Deleting this file is safe: it is recreated, with the
# then-current defaults, on the next server start. Explicit soma daemon
# flags take precedence over this file.

#server:
#  # Exit the playback server after this long with no connected clients and
#  # stopped playback. Same as the --idle-timeout flag.
#  #   "0"                      never exit on idle; the server runs until
#  #                            stopped explicitly (the default)
#  #   "90s", "5m", "1h30m"     exit after that long idle (Go duration syntax)
#  idle_timeout: %s
#
#  # Show the system tray / menu-bar icon while the server runs.
#  # "tray: false" is the same as the --no-tray flag.
#  tray: true
#
#  # Also listen for frontends on TCP (host:port), e.g. to control this
#  # machine's playback from a laptop. Same as the --listen flag. The Unix
#  # socket stays available either way; empty disables TCP (the default).
#  listen: "0.0.0.0:5454"
#
#  # Encrypt the TCP listener with TLS. Without tls_cert/tls_key a
#  # self-signed certificate is generated in the state directory and its
#  # fingerprint printed at startup ("soma daemon --show-cert" reprints it).
#  tls: true
#
#  # Use this PEM certificate/key pair instead (setting them implies TLS).
#  tls_cert: /path/to/cert.pem
#  tls_key: /path/to/key.pem
#
#  # Require TCP clients to know this pre-shared key (the Unix socket is
#  # exempt). To keep the secret out of this file, set instead
#  #   psk_file: /path/to/psk
#  # (or the --psk-file flag); psk and psk_file are mutually exclusive.
#  psk: "change-me"
#
#client:
#  # Connect the TUI and CLI to a remote soma daemon instead of the local
#  # Unix socket. Same as the --server flag or $SOMAD_SERVER.
#  server: "myserver:5454"
#
#  # Use TLS for the connection (implied by tls_ca or tls_fingerprint;
#  # with neither of those, the system trust store must know the server's
#  # certificate). Same as the --tls flag.
#  tls: true
#
#  # Trust the server by pinning the SHA-256 fingerprint it prints at
#  # startup — or, mutually exclusive with that, by certificate/CA file:
#  #   tls_ca: /path/to/cert.pem
#  tls_fingerprint: "sha256:..."
#
#  # Pre-shared key matching the server's psk setting (or psk_file, see
#  # above; mutually exclusive).
#  psk: "change-me"
#
#tui:
#  # Stop playback and shut down the server when closing the TUI.
#  # Same as the --shutdown-on-exit flag.
#  shutdown_on_exit: false
`

// EnsureTemplate writes the commented-out default template to Path() when no
// config file exists yet, so the settings are discoverable without the docs.
// It never touches an existing file. It reports the path it considered and
// whether it created the file.
func EnsureTemplate(defaultIdleTimeout time.Duration) (path string, created bool, err error) {
	path, err = Path()
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return path, false, fmt.Errorf("failed to create config directory: %w", err)
	}
	// O_EXCL makes "create only if missing" atomic, so a user file can never
	// be clobbered and concurrent server spawns cannot race each other.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path derived from the user config dir, not user input
	if err != nil {
		if os.IsExist(err) {
			return path, false, nil
		}
		return path, false, fmt.Errorf("failed to create config file: %w", err)
	}
	_, werr := fmt.Fprintf(f, templateFormat, defaultIdleTimeout)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		// Remove the partial file so the next server start retries cleanly.
		_ = os.Remove(path)
		return path, false, fmt.Errorf("failed to write config file: %w", werr)
	}
	return path, true, nil
}
