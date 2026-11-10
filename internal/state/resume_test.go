package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"somad/internal/atomicfile"
)

func TestResume_IsTakenOnce(t *testing.T) {
	dir := SetStateDir(t)
	stopAt := time.Now().Add(time.Hour).Truncate(time.Second)
	require.NoError(t, SaveResume(Resume{ChannelID: "groovesalad", StopAt: stopAt}))

	r, ok := TakeResume(time.Minute)

	require.True(t, ok)
	assert.Equal(t, "groovesalad", r.ChannelID)
	assert.True(t, stopAt.Equal(r.StopAt))
	_, err := os.Stat(filepath.Join(dir, appDirName, resumeFileName))
	assert.True(t, os.IsNotExist(err), "it is for one start only")
	_, ok = TakeResume(time.Minute)
	assert.False(t, ok)
}

func TestResume_NoneWithoutAFile(t *testing.T) {
	SetStateDir(t)
	_, ok := TakeResume(time.Minute)
	assert.False(t, ok)
}

func TestResume_WithoutASleepTimerHasNoStopTime(t *testing.T) {
	SetStateDir(t)
	require.NoError(t, SaveResume(Resume{ChannelID: "groovesalad"}))

	r, ok := TakeResume(time.Minute)

	require.True(t, ok)
	assert.True(t, r.StopAt.IsZero())
}

// A note that is not from a restart just now must not make a daemon play
// on its own: it is dropped, and removed all the same.
func TestResume_IsDroppedWhenItIsNotFromJustNow(t *testing.T) {
	for name, content := range map[string]any{
		"written long ago":      Resume{ChannelID: "groovesalad", At: time.Now().Add(-time.Hour)},
		"written in the future": Resume{ChannelID: "groovesalad", At: time.Now().Add(time.Hour)},
		"names no channel":      Resume{At: time.Now()},
		"not a note at all":     []string{"groovesalad"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := SetStateDir(t)
			path := filepath.Join(dir, appDirName, resumeFileName)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, atomicfile.WriteJSON(path, content, 0o600))

			_, ok := TakeResume(time.Minute)

			assert.False(t, ok)
			_, err := os.Stat(path)
			assert.True(t, os.IsNotExist(err), "nor is it left for the next start")
		})
	}
}
