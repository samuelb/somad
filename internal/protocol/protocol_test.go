package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteLine_RoundTripRequest(t *testing.T) {
	var buf bytes.Buffer
	params, err := json.Marshal(PlayParams{ChannelID: "groovesalad"})
	require.NoError(t, err)

	require.NoError(t, WriteLine(&buf, Request{ID: 7, Method: MethodPlay, Params: params}))

	sc := NewScanner(&buf)
	require.True(t, sc.Scan())

	var req Request
	require.NoError(t, json.Unmarshal(sc.Bytes(), &req))
	assert.Equal(t, int64(7), req.ID)
	assert.Equal(t, MethodPlay, req.Method)

	var play PlayParams
	require.NoError(t, json.Unmarshal(req.Params, &play))
	assert.Equal(t, "groovesalad", play.ChannelID)
}

func TestServerMessage_DemuxResponse(t *testing.T) {
	var buf bytes.Buffer
	result, err := json.Marshal(PlaybackState{Status: StatusPlaying, ChannelID: "dronezone", Volume: 0.8})
	require.NoError(t, err)
	require.NoError(t, WriteLine(&buf, Response{ID: 3, Result: result}))

	var msg ServerMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &msg))
	require.NotNil(t, msg.ID)
	assert.Equal(t, int64(3), *msg.ID)
	assert.Empty(t, msg.Event)

	var st PlaybackState
	require.NoError(t, json.Unmarshal(msg.Result, &st))
	assert.Equal(t, StatusPlaying, st.Status)
	assert.Equal(t, "dronezone", st.ChannelID)
	assert.InDelta(t, 0.8, st.Volume, 1e-9)
}

func TestServerMessage_DemuxEvent(t *testing.T) {
	var buf bytes.Buffer
	data, err := json.Marshal(PlaybackState{Status: StatusStopped, Volume: 1})
	require.NoError(t, err)
	require.NoError(t, WriteLine(&buf, Event{Event: EventState, Data: data}))

	var msg ServerMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &msg))
	assert.Nil(t, msg.ID)
	assert.Equal(t, EventState, msg.Event)
}

func TestServerMessage_ResponseIDZeroIsStillResponse(t *testing.T) {
	// A response with ID 0 must not be mistaken for an event: the id field
	// is always serialized (no omitempty on Response.ID).
	var buf bytes.Buffer
	require.NoError(t, WriteLine(&buf, Response{ID: 0, Error: "boom"}))

	var msg ServerMessage
	require.NoError(t, json.Unmarshal(buf.Bytes(), &msg))
	require.NotNil(t, msg.ID)
	assert.Equal(t, int64(0), *msg.ID)
	assert.Equal(t, "boom", msg.Error)
}

func TestNewScanner_LargeLine(t *testing.T) {
	// Larger than the default bufio.Scanner limit (64 KB), smaller than ours.
	big := strings.Repeat("x", 1<<20)
	var buf bytes.Buffer
	require.NoError(t, WriteLine(&buf, Event{Event: EventChannels, Data: json.RawMessage(`"` + big + `"`)}))

	sc := NewScanner(&buf)
	require.True(t, sc.Scan(), "scanner should handle a 1 MiB line")
	require.NoError(t, sc.Err())
}

func TestSocketPath_EnvOverride(t *testing.T) {
	t.Setenv("SOMAD_SOCKET", "/tmp/custom.sock")
	assert.Equal(t, "/tmp/custom.sock", SocketPath())
}

func TestSocketPath_XDGRuntimeDir(t *testing.T) {
	t.Setenv("SOMAD_SOCKET", "")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	assert.Equal(t, "/run/user/1000/somad.sock", SocketPath())
}

func TestSocketPath_FallbackFitsSunPathLimit(t *testing.T) {
	t.Setenv("SOMAD_SOCKET", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	p := SocketPath()
	assert.NotEmpty(t, p)
	// macOS caps sun_path at 104 bytes; keep headroom.
	assert.LessOrEqual(t, len(p), 100, "socket path too long: %s", p)
}

func TestLockPath(t *testing.T) {
	assert.Equal(t, "/x/somad.sock.lock", LockPath("/x/somad.sock"))
}

func TestEnsureSocketDir_CreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, EnsureSocketDir(filepath.Join(dir, "somad.sock")))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestEnsureSocketDir_RejectsGroupAccessibleDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.Mkdir(dir, 0o755)) // #nosec G301 -- intentionally permissive for rejection test

	err := EnsureSocketDir(filepath.Join(dir, "somad.sock"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be accessible")
}
