package xdg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clearXDGEnv unsets every XDG override this package looks at, so a test
// exercising the OS-default fallback isn't at the mercy of the ambient
// environment (e.g. a devcontainer setting $XDG_CACHE_HOME).
func clearXDGEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
}

func TestConfigDir_UsesXDGOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	dir, err := ConfigDir("somad")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/custom/config", "somad"), dir)
}

func TestStateDir_UsesXDGOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	dir, err := StateDir("somad")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/custom/state", "somad"), dir)
}

func TestCacheDir_UsesXDGOverride(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	dir, err := CacheDir("somad")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/custom/cache", "somad"), dir)
}

func TestConfigDir_DefaultsMatchPlatform(t *testing.T) {
	clearXDGEnv(t)
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	dir, err := ConfigDir("somad")

	require.NoError(t, err)
	if runtime.GOOS == "darwin" {
		assert.Equal(t, filepath.Join(home, "Library", "Application Support", "somad"), dir)
	} else {
		assert.Equal(t, filepath.Join(home, ".config", "somad"), dir)
	}
}

func TestStateDir_DefaultsMatchPlatform(t *testing.T) {
	clearXDGEnv(t)
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	dir, err := StateDir("somad")

	require.NoError(t, err)
	if runtime.GOOS == "darwin" {
		assert.Equal(t, filepath.Join(home, "Library", "Application Support", "somad"), dir)
	} else {
		assert.Equal(t, filepath.Join(home, ".local", "state", "somad"), dir)
	}
}

func TestCacheDir_DefaultsMatchPlatform(t *testing.T) {
	clearXDGEnv(t)
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	dir, err := CacheDir("somad")

	require.NoError(t, err)
	if runtime.GOOS == "darwin" {
		assert.Equal(t, filepath.Join(home, "Library", "Caches", "somad"), dir)
	} else {
		assert.Equal(t, filepath.Join(home, ".cache", "somad"), dir)
	}
}
