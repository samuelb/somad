// Package xdg resolves the per-user base directories somad's config,
// state, and cache packages each need, honoring the XDG Base Directory
// environment variables on every platform (which also lets tests isolate
// themselves without touching the real home directory) and falling back to
// the conventional Linux and macOS locations otherwise.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ConfigDir returns the base directory for app's configuration files.
// Linux/other: $XDG_CONFIG_HOME/<app> or ~/.config/<app>
// macOS: ~/Library/Application Support/<app> ($XDG_CONFIG_HOME still
// overrides, so tests can isolate themselves on any platform)
func ConfigDir(app string) (string, error) {
	return dir("XDG_CONFIG_HOME", []string{".config"}, []string{"Library", "Application Support"}, app)
}

// StateDir returns the base directory for app's persisted state.
// Linux/other: $XDG_STATE_HOME/<app> or ~/.local/state/<app>
// macOS: ~/Library/Application Support/<app>
func StateDir(app string) (string, error) {
	return dir("XDG_STATE_HOME", []string{".local", "state"}, []string{"Library", "Application Support"}, app)
}

// CacheDir returns the base directory for app's cached (safely
// re-fetchable) files.
// Linux/other: $XDG_CACHE_HOME/<app> or ~/.cache/<app>
// macOS: ~/Library/Caches/<app>
func CacheDir(app string) (string, error) {
	return dir("XDG_CACHE_HOME", []string{".cache"}, []string{"Library", "Caches"}, app)
}

// dir resolves <base>/app, where base is the environment variable envVar
// when set (checked first on every platform, which is also what enables
// tests to isolate themselves), or otherwise a fixed path under the home
// directory: otherRel on Linux and other Unix-like systems, darwinRel on
// macOS.
func dir(envVar string, otherRel, darwinRel []string, app string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return filepath.Join(v, app), nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	rel := otherRel
	if runtime.GOOS == "darwin" {
		rel = darwinRel
	}
	parts := append([]string{homeDir}, rel...)
	parts = append(parts, app)
	return filepath.Join(parts...), nil
}
