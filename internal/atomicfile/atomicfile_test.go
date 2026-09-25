package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFile_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")

	require.NoError(t, WriteFile(path, []byte("hello"), 0o600))

	data, err := os.ReadFile(path) // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteFile_ReplacesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.json")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	require.NoError(t, WriteFile(path, []byte("new"), 0o600))

	data, err := os.ReadFile(path) // #nosec G304 // Test file path
	require.NoError(t, err)
	assert.Equal(t, "new", string(data))
}

func TestWriteFile_LeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	require.NoError(t, WriteFile(path, []byte("hello"), 0o600))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "out.json", entries[0].Name())
}

func TestWriteFile_MissingDirectoryFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "out.json")

	err := WriteFile(path, []byte("hello"), 0o600)
	assert.Error(t, err)
}

func TestCreateExclusive_CreatesCompleteFileAtPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")

	created, err := CreateExclusive(path, 0o600, func(w io.Writer) error {
		_, err := io.WriteString(w, "secret")
		return err
	})

	require.NoError(t, err)
	assert.True(t, created)
	data, err := os.ReadFile(path) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "secret", string(data))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assertOnlyEntry(t, path)
}

func TestCreateExclusive_NeverTouchesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("mine"), 0o600))

	created, err := CreateExclusive(path, 0o600, func(w io.Writer) error {
		_, err := io.WriteString(w, "template")
		return err
	})

	require.NoError(t, err)
	assert.False(t, created)
	data, err := os.ReadFile(path) // #nosec G304 -- test temp path
	require.NoError(t, err)
	assert.Equal(t, "mine", string(data))
	assertOnlyEntry(t, path)
}

// A failed write must leave nothing behind: neither a partial file at path
// (which would block the retry) nor a temp file.
func TestCreateExclusive_FailedWriteLeavesNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "psk")

	created, err := CreateExclusive(path, 0o600, func(w io.Writer) error {
		_, _ = io.WriteString(w, "half")
		return errors.New("boom")
	})

	require.Error(t, err)
	assert.False(t, created)
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// assertOnlyEntry checks that path is the only entry in its directory, i.e.
// no temp file was left behind.
func assertOnlyEntry(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, filepath.Base(path), entries[0].Name())
}
