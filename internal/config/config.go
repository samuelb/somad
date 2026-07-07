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
	appDirName     = "somatui"
)

// Config is the parsed configuration file. Fields are pointers so an
// explicit zero value ("tray: false", "idle_timeout: 0") is distinguishable
// from an absent key, which falls back to the built-in default.
type Config struct {
	Server ServerConfig `yaml:"server"`
}

// ServerConfig configures the playback server, mirroring the flags of
// `somatui server`.
type ServerConfig struct {
	// IdleTimeout is how long the server lingers with no connected clients
	// and stopped playback before exiting; 0 disables the timeout.
	IdleTimeout *Duration `yaml:"idle_timeout"`
	// Tray controls the system tray / menu-bar icon (the inverse of the
	// --no-tray flag, so the file reads positively).
	Tray *bool `yaml:"tray"`
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
// On Linux: $XDG_CONFIG_HOME/somatui/config.yaml or ~/.config/somatui/config.yaml
// On macOS: ~/Library/Application Support/somatui/config.yaml
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
	return &cfg, nil
}

// templateFormat is the generated default config file. Every setting is
// commented out, so parsing it yields the built-in defaults even when those
// change in a later release.
const templateFormat = `# SomaTUI configuration file.
#
# Generated with the built-in defaults, everything commented out; uncomment a
# setting to change it. Deleting this file is safe: it is recreated, with the
# then-current defaults, on the next server start. Explicit somatui server
# flags take precedence over this file.

#server:
#  # Exit the playback server after this long with no connected clients and
#  # stopped playback. Go duration syntax ("90s", "5m", "1h30m"); "0"
#  # disables the timeout. Same as the --idle-timeout flag.
#  idle_timeout: %s
#
#  # Show the system tray / menu-bar icon while the server runs.
#  # "tray: false" is the same as the --no-tray flag.
#  tray: true
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
