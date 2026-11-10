package audio

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"somad/internal/security/securitytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testGen hands out increasing generation numbers for Play and Stop, the
// way the server's counter does.
var testGen atomic.Uint64

// playNext calls Play with the next generation.
func playNext(p *AudioPlayer, url, format string) error {
	return p.Play(url, format, testGen.Add(1))
}

// stopNext calls Stop with the next generation.
func stopNext(p *AudioPlayer) {
	p.Stop(testGen.Add(1))
}

// newTestPlayer returns a bare AudioPlayer without an oto context. This is
// enough for tests that exercise methods which never touch the audio device.
func newTestPlayer() *AudioPlayer {
	return &AudioPlayer{
		userAgent:      "soma/test",
		errChan:        make(chan error, 2),
		trackChan:      make(chan TrackInfo, 1),
		stallTimeout:   streamStallTimeout,
		connectTimeout: streamConnectTimeout,
		prefillWait:    streamBufferPrefillWait,
		readyTimeout:   audioReadyTimeout,
		devicePoll:     devicePollInterval,
	}
}

type fakeOutputPlayer struct {
	mu             sync.Mutex
	volume         float64
	paused         func()
	stoppedReading func()
	// buffered is BufferedSize; nil for a player that holds no audio.
	buffered func() int
	// lowered counts the SetVolume calls that turned the volume down: one
	// when a session is committed, at volume 0, and the steps of a fade-out.
	lowered atomic.Int32

	// reading is held for each read of the source, so PauseAndStopReading
	// waits one out, as oto's does, before it sets outOfMixer.
	reading    sync.Mutex
	outOfMixer bool // guarded by reading
}

func (p *fakeOutputPlayer) Play() {}

func (p *fakeOutputPlayer) Pause() {
	if p.paused != nil {
		p.paused()
	}
}

func (p *fakeOutputPlayer) PauseAndStopReading() {
	p.reading.Lock()
	p.outOfMixer = true
	p.reading.Unlock()
	if p.stoppedReading != nil {
		p.stoppedReading()
	}
}

// pump consumes r on behalf of the player until the first read error or
// PauseAndStopReading, like oto's mixer does.
func (p *fakeOutputPlayer) pump(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		p.reading.Lock()
		if p.outOfMixer {
			p.reading.Unlock()
			return
		}
		_, err := r.Read(buf)
		p.reading.Unlock()
		if err != nil {
			return
		}
	}
}

func (p *fakeOutputPlayer) SetVolume(v float64) {
	p.mu.Lock()
	if v < p.volume {
		p.lowered.Add(1)
	}
	p.volume = v
	p.mu.Unlock()
}

func (p *fakeOutputPlayer) BufferedSize() int {
	if p.buffered == nil {
		return 0
	}
	return p.buffered()
}

func (p *fakeOutputPlayer) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

type fakeAudioContext struct {
	suspends       atomic.Int32
	resumes        atomic.Int32
	players        atomic.Int32
	pauses         atomic.Int32
	stoppedReading atomic.Int32 // players taken out of the mixer
	// pump makes each player consume its reader on a goroutine until the
	// first read error or PauseAndStopReading, like oto's mixer does.
	pump bool
	// playedOut makes the players report an empty buffer, like one that has
	// played everything it had read from its stream. Otherwise they hold
	// audio, as a player does whose stream keeps up.
	playedOut atomic.Bool

	mu        sync.Mutex
	resumeErr error
	// fatal is the context's fatal error once fail has set one: Err returns
	// it from then on, and Suspend and Resume fail with it, as oto's do.
	fatal error
}

func (c *fakeAudioContext) NewPlayer(r io.Reader) outputPlayer {
	c.players.Add(1)
	p := &fakeOutputPlayer{
		volume:         1,
		paused:         func() { c.pauses.Add(1) },
		stoppedReading: func() { c.stoppedReading.Add(1) },
		buffered: func() int {
			if c.playedOut.Load() {
				return 0
			}
			return 4096
		},
	}
	if c.pump {
		go p.pump(r)
	}
	return p
}

func (c *fakeAudioContext) Suspend() error {
	c.suspends.Add(1)
	return c.Err()
}

func (c *fakeAudioContext) Resume() error {
	c.resumes.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fatal != nil {
		return c.fatal
	}
	return c.resumeErr
}

func (c *fakeAudioContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fatal
}

// fail makes the device fail for good, like a sound server going away.
func (c *fakeAudioContext) fail(err error) {
	c.mu.Lock()
	c.fatal = err
	c.mu.Unlock()
}

func (c *fakeAudioContext) setResumeError(err error) {
	c.mu.Lock()
	c.resumeErr = err
	c.mu.Unlock()
}

func newLifecycleTestPlayer(t *testing.T) (*AudioPlayer, *fakeAudioContext, *atomic.Int32) {
	t.Helper()
	// The streaming test server sends less than the prefill and then holds
	// the connection, so without this every Play would wait out the full
	// prefill deadline.
	p := NewPlayer("soma/test")
	p.prefillWait = 10 * time.Millisecond
	ctx := &fakeAudioContext{}
	created := &atomic.Int32{}
	ready := make(chan struct{})
	close(ready)
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		created.Add(1)
		return ctx, ready, nil
	}
	return p, ctx, created
}

func newStreamingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	server, _ := newCountingStreamServer(t)
	return server
}

// newCountingStreamServer is newStreamingTestServer with a count of the
// requests that reached it.
func newCountingStreamServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	securitytest.AllowTestHosts(t)
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(silentMP3Frames(30))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func TestNewPlayer_DoesNotOpenAudioDevice(t *testing.T) {
	_, _, created := newLifecycleTestPlayer(t)
	assert.Zero(t, created.Load())
}

func TestPlayStop_ResumesAndSuspendsAudioDevice(t *testing.T) {
	p, ctx, created := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	assert.EqualValues(t, 1, created.Load())
	assert.EqualValues(t, 1, ctx.players.Load())
	assert.Zero(t, ctx.resumes.Load(), "a new context is already active")
	assert.Zero(t, ctx.suspends.Load())

	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	assert.EqualValues(t, 1, ctx.resumes.Load())
	assert.EqualValues(t, 2, ctx.players.Load())
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 2
	}, time.Second, 10*time.Millisecond)
}

func TestEnsureContext_RecoversAfterReadyTimeout(t *testing.T) {
	p := NewPlayer("soma/test")
	ctx := &fakeAudioContext{}
	ready := make(chan struct{})
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		return ctx, ready, nil
	}
	p.readyTimeout = 20 * time.Millisecond

	err := p.ensureContext(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audio device not ready")
	// Slow is not lost: the device may still come up, so the caller may
	// try again, and nothing is held against a later Play.
	assert.ErrorIs(t, err, ErrAudioDevice)
	assert.NotErrorIs(t, err, ErrAudioDeviceLost)
	assert.NoError(t, p.DeviceErr())

	close(ready)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, p.ensureContext(context.Background()))
}

// slowDevice makes p's audio device come up only once the returned ready
// channel is closed, like a hung audio daemon; waiting is closed when the
// first Play starts waiting for it. The ready timeout stays the default,
// seconds away.
func slowDevice(p *AudioPlayer, ctx *fakeAudioContext) (ready, waiting chan struct{}) {
	ready, waiting = make(chan struct{}), make(chan struct{})
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		close(waiting)
		return ctx, ready, nil
	}
	return ready, waiting
}

func TestPlay_StopEndsTheWaitForTheAudioDevice(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })
	ready, waiting := slowDevice(p, ctx)

	done := playAsync(p, server.URL, FormatMP3, testGen.Add(1))
	<-waiting
	stopNext(p)
	// Only the Stop can end Play this soon: the stream has decoded, and
	// the device's timeout is seconds away.
	assert.ErrorIs(t, awaitPlay(t, done, time.Second), ErrSuperseded)

	// The device comes up after all, with nothing to play, so it is
	// suspended, and the next Play resumes it.
	close(ready)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	assert.EqualValues(t, 1, ctx.resumes.Load())
	assert.EqualValues(t, 1, ctx.players.Load())
}

func TestPlay_NewerPlayEndsTheWaitForTheAudioDevice(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })
	ready, waiting := slowDevice(p, ctx)

	older := playAsync(p, server.URL, FormatMP3, testGen.Add(1))
	<-waiting
	newer := playAsync(p, server.URL, FormatMP3, testGen.Add(1))
	assert.ErrorIs(t, awaitPlay(t, older, time.Second), ErrSuperseded)

	// The newer Play waits for the same device, and plays once it is up.
	close(ready)
	require.NoError(t, awaitPlay(t, newer, time.Second))
	assert.EqualValues(t, 1, ctx.players.Load())
}

func TestPlay_ResumeErrorDoesNotCommitSession(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)

	ctx.setResumeError(errors.New("resume failed"))
	err := playNext(p, server.URL, FormatMP3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resume audio device")
	assert.ErrorIs(t, err, ErrAudioDevice)
	assert.EqualValues(t, 1, ctx.players.Load())
	p.mu.Lock()
	assert.Nil(t, p.current)
	assert.Zero(t, p.sessions)
	p.mu.Unlock()

	ctx.setResumeError(nil)
	require.NoError(t, playNext(p, server.URL, FormatMP3))
}

func TestPlay_DeviceFailureIsMarked(t *testing.T) {
	p, _, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		return nil, nil, errors.New("no audio hardware")
	}

	// The stream decoded fine; the device is what failed, so the error must
	// say so for the caller to stop trying other streams.
	err := playNext(p, server.URL, FormatMP3)
	require.ErrorIs(t, err, ErrAudioDevice)
	assert.Contains(t, err.Error(), "no audio hardware")
	// oto creates one context per process, so there is no second try.
	assert.ErrorIs(t, err, ErrAudioDeviceLost)
	assert.ErrorIs(t, p.DeviceErr(), ErrAudioDeviceLost)
}

func TestPlay_DeviceLostWhilePlayingIsReportedOnce(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	p.devicePoll = 20 * time.Millisecond
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	gen := testGen.Add(1)
	require.NoError(t, p.Play(server.URL, FormatMP3, gen))
	require.NoError(t, p.DeviceErr())

	// The sound server goes away under a playing session. The stream is
	// healthy and nothing reads the device's error but the session's poll;
	// without it playback would go on in silence under a "playing" status.
	ctx.fail(errors.New("connection closed"))

	select {
	case err := <-p.Errors():
		assert.ErrorIs(t, err, ErrAudioDeviceLost)
		assert.ErrorIs(t, err, ErrAudioDevice, "a lost device is a device error too")
		assert.EqualError(t, err, "audio device failed: connection closed")
		var se *StreamError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, gen, se.Gen, "reported for the session that was playing")
	case <-time.After(fadeInDuration + time.Second):
		t.Fatal("the device failure was never reported")
	}

	// The session lives on until the caller stops it, and must not report
	// the same failure at every poll.
	select {
	case err := <-p.Errors():
		t.Fatalf("the device failure was reported again: %v", err)
	case <-time.After(10 * p.devicePoll):
	}
	assert.ErrorIs(t, p.DeviceErr(), ErrAudioDeviceLost)
}

func TestPlay_LostDeviceFailsBeforeConnecting(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server, requests := newCountingStreamServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.EqualValues(t, 1, requests.Load())
	ctx.fail(errors.New("connection closed"))

	// No stream can play on a device that is gone, so Play says so without
	// connecting to one, and words it as a device that failed: it had
	// initialized fine.
	for range 2 {
		err := playNext(p, server.URL, FormatMP3)
		require.ErrorIs(t, err, ErrAudioDeviceLost)
		assert.ErrorIs(t, err, ErrAudioDevice)
		assert.EqualError(t, err, "audio device failed: connection closed")
	}
	assert.EqualValues(t, 1, requests.Load(), "Play connected to a stream it could not play")
	assert.EqualValues(t, 1, ctx.players.Load())
}

func TestPlay_DeviceThatFailsToInitializeIsLost(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server, requests := newCountingStreamServer(t)
	// The context is ready, with an error: no sound server to talk to.
	ctx.fail(errors.New("no sound server"))

	// The device is opened by the first Play, which has connected by then;
	// from the second on the failure is known beforehand.
	for range 2 {
		err := playNext(p, server.URL, FormatMP3)
		require.ErrorIs(t, err, ErrAudioDeviceLost)
		assert.EqualError(t, err, "failed to initialize audio device: no sound server")
	}
	assert.EqualValues(t, 1, requests.Load())
	assert.Zero(t, ctx.players.Load())
}

// resumeKillsDevice is a device that fails for good when it is resumed:
// the failure falls between Play's last look at the device and the resume.
type resumeKillsDevice struct{ *fakeAudioContext }

func (c resumeKillsDevice) Resume() error {
	c.fail(errors.New("connection closed"))
	return c.fakeAudioContext.Resume()
}

func TestPlay_ResumeFailingForGoodIsDeviceLost(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })
	ready := make(chan struct{})
	close(ready)
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		return resumeKillsDevice{ctx}, ready, nil
	}

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)

	// A resume that fails with the device's fatal error is not the kind a
	// later Play gets past.
	err := playNext(p, server.URL, FormatMP3)
	require.ErrorIs(t, err, ErrAudioDeviceLost)
	assert.EqualError(t, err, "audio device failed: connection closed")
	assert.EqualValues(t, 1, ctx.players.Load())
}

func TestStop_SuspendFailingForGoodIsKept(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	ctx.fail(errors.New("connection closed"))
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)

	// The session ended before its poll came round, and the suspend that
	// followed failed with the device's fatal error. That is the last the
	// player hears of the device until the next Play, so it must keep it.
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return errors.Is(p.deviceErr, ErrAudioDeviceLost)
	}, time.Second, 10*time.Millisecond)
}

func TestPlaySwitch_DoesNotSuspendReplacementSession(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.Eventually(t, func() bool {
		return ctx.pauses.Load() == 1
	}, time.Second, 10*time.Millisecond)

	// The old session has completed teardown. It must see the new current
	// session and leave the context running.
	assert.Zero(t, ctx.suspends.Load())
	assert.Zero(t, ctx.resumes.Load())

	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.pauses.Load() == 2 && ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestStopDuringCrossfade_WaitsForBothSessionsBeforeSuspend(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)

	require.Eventually(t, func() bool {
		return ctx.pauses.Load() >= 1
	}, time.Second, 10*time.Millisecond)
	if ctx.pauses.Load() == 1 {
		assert.Zero(t, ctx.suspends.Load(), "one session is still draining")
	}
	require.Eventually(t, func() bool {
		return ctx.pauses.Load() == 2 && ctx.suspends.Load() == 1
	}, time.Second, 10*time.Millisecond)
}

func TestTeardown_TakesThePlayerOutOfTheMixer(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	// Pumped like oto's mixer does: the test server sends a burst and then
	// holds the connection, so by the end of the fade-out the pump is
	// parked in a read the stalled stream will not answer.
	ctx.pump = true
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)

	// Paused alone, the player would stay in oto's mixer, its source chain
	// with it, until a GC cleanup. Taking it out waits out that read, so
	// it must come after the stream is closed: before, it would hold the
	// teardown until the stall watchdog fired, 30 s on.
	require.Eventually(t, func() bool {
		return ctx.stoppedReading.Load() == 1 && ctx.suspends.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)
	assert.EqualValues(t, 1, ctx.pauses.Load())
}

// currentSession returns the session p is playing and its fake oto player.
func currentSession(t *testing.T, p *AudioPlayer) (*session, *fakeOutputPlayer) {
	t.Helper()
	p.mu.Lock()
	s := p.current
	p.mu.Unlock()
	require.NotNil(t, s)
	return s, s.player.(*fakeOutputPlayer)
}

func TestPlaySwitch_SessionWithNothingLeftToPlayIsNotFaded(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	// Pumped like oto's mixer does. The test server sends a burst and then
	// holds the connection: the stream has stalled, the pump is parked in a
	// read the stream will not answer, and the player has played out what
	// it had.
	ctx.pump = true
	ctx.playedOut.Store(true)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	old, player := currentSession(t, p)
	// Played out, not yet to start: a tap that has taken no audio at all
	// counts as stalled too.
	require.Eventually(t, func() bool {
		old.tap.mu.Lock()
		played := old.tap.written > 0
		old.tap.mu.Unlock()
		return played && old.drained()
	}, 2*time.Second, 10*time.Millisecond)
	steps := player.lowered.Load()

	// oto fills all its players from the one goroutine that is parked in
	// the old stream's read, so the new session gets no audio until the old
	// stream is closed. Fading silence for a quarter of a second first
	// would cost the new session the first half of its fade-in.
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	switched := time.Now()
	require.Eventually(t, func() bool {
		return ctx.stoppedReading.Load() == 1
	}, 2*time.Second, time.Millisecond)
	assert.Less(t, time.Since(switched), fadeOutDuration, "the old stream was held open for the length of a fade")
	assert.Zero(t, player.lowered.Load()-steps, "the old session was faded, with nothing left to fade")
}

// fadeOutSteps stops p's session and returns how many steps down its
// volume was taken on the way out.
func fadeOutSteps(t *testing.T, p *AudioPlayer, ctx *fakeAudioContext, player *fakeOutputPlayer) int32 {
	t.Helper()
	before := player.lowered.Load()
	stopNext(p)
	require.Eventually(t, func() bool {
		return ctx.stoppedReading.Load() == 1
	}, 2*time.Second, 5*time.Millisecond)
	return player.lowered.Load() - before
}

func TestStop_StalledStreamWithAudioBufferedStillFadesOut(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	// The stream has stalled, as above, but the player still holds audio it
	// read ahead of the stall. That plays on, and is faded, not cut off.
	ctx.pump = true
	server := newStreamingTestServer(t)

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	s, player := currentSession(t, p)
	require.Eventually(t, func() bool {
		s.tap.mu.Lock()
		played := s.tap.written > 0
		s.tap.mu.Unlock()
		return played && s.tap.stalled()
	}, 2*time.Second, 10*time.Millisecond)

	assert.EqualValues(t, fadeSteps, fadeOutSteps(t, p, ctx, player))
	assert.Zero(t, player.Volume())
}

// endlessSilence decodes to silence for as long as it is read, a little at
// a time: a stream that keeps up.
type endlessSilence struct{}

func (endlessSilence) Read(b []byte) (int, error) {
	time.Sleep(time.Millisecond)
	clear(b)
	return len(b), nil
}

func (endlessSilence) SampleRate() int { return sampleRate }

func TestStop_FlowingStreamStillFadesOut(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	// The player's buffer is empty at every look, as in an underrun, but
	// the stream is delivering: audio is about to play, and is faded.
	ctx.pump = true
	ctx.playedOut.Store(true)
	server := newStreamingTestServer(t)
	prevDecoder := newDecoder
	newDecoder = func(_ string, r io.Reader) (pcmDecoder, error) {
		// Drain the pipe on a goroutine so the fetch side never blocks; the
		// decoder's own output does not depend on it.
		go func() { _, _ = io.Copy(io.Discard, r) }()
		return endlessSilence{}, nil
	}
	t.Cleanup(func() { newDecoder = prevDecoder })

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	_, player := currentSession(t, p)
	// A fade-out starts from where the fade-in has brought the volume.
	require.Eventually(t, func() bool { return player.Volume() > 0 }, time.Second, 5*time.Millisecond)

	assert.EqualValues(t, fadeSteps, fadeOutSteps(t, p, ctx, player))
	assert.Zero(t, player.Volume())
}

func TestErrors_ReturnsChannel(t *testing.T) {
	p := newTestPlayer()
	assert.NotNil(t, p.Errors())
}

func TestReportError_NilError(t *testing.T) {
	p := newTestPlayer()

	p.reportError(context.Background(), 1, nil)

	select {
	case err := <-p.errChan:
		t.Fatalf("nil error should not be sent, got: %v", err)
	default:
	}
}

func TestReportError_CancelledContext(t *testing.T) {
	p := newTestPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p.reportError(ctx, 1, errors.New("boom"))

	select {
	case err := <-p.errChan:
		t.Fatalf("error should be suppressed when context is cancelled, got: %v", err)
	default:
	}
}

func TestReportError_Delivers(t *testing.T) {
	p := newTestPlayer()

	p.reportError(context.Background(), 1, errors.New("stream failed"))

	select {
	case err := <-p.errChan:
		assert.EqualError(t, err, "stream failed")
	default:
		t.Fatal("expected error to be delivered")
	}
}

func TestReportError_FullChannelDoesNotBlock(t *testing.T) {
	p := newTestPlayer()

	// Fill the buffered channel (capacity 2), then a third report must not block.
	p.reportError(context.Background(), 1, errors.New("1"))
	p.reportError(context.Background(), 1, errors.New("2"))
	p.reportError(context.Background(), 1, errors.New("3")) // dropped, must not block

	assert.Len(t, p.errChan, 2)
}

// drainPipe reads everything from r until EOF or error, returning the bytes read
// and the terminating error.
func drainPipe(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

// silentMP3Frames returns n silent MPEG-1 Layer III frames (44.1 kHz, 128 kbps,
// stereo): a sync header followed by all-zero side info and main data.
func silentMP3Frames(n int) []byte {
	const frameSize = 417 // 144 * 128000 / 44100
	frame := make([]byte, frameSize)
	frame[0], frame[1], frame[2], frame[3] = 0xFF, 0xFB, 0x90, 0x64
	buf := make([]byte, 0, n*frameSize)
	for i := 0; i < n; i++ {
		buf = append(buf, frame...)
	}
	return buf
}

func TestPlay_SupersededByStop(t *testing.T) {
	securitytest.AllowTestHosts(t)

	// Hold the stream response until the test has issued Stop, so the Play
	// call is still connecting when it is superseded.
	requestArrived := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestArrived)
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write(silentMP3Frames(30))
	}))
	defer server.Close()

	// No oto context: the superseded path must return before touching it.
	p := newTestPlayer()

	playErr := make(chan error, 1)
	go func() { playErr <- playNext(p, server.URL, FormatMP3) }()

	<-requestArrived
	stopNext(p) // supersedes the in-flight Play
	close(release)

	err := <-playErr
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSuperseded)
}

func TestFetchStream_Success(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "soma/test", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("audio-bytes"))
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw, committedStream())

	data, err := drainPipe(pr)
	require.NoError(t, err)
	assert.Equal(t, "audio-bytes", string(data))

	// A live stream ending (clean EOF) is abnormal and must be reported so
	// the reconnect machinery kicks in instead of playing silence.
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream ended unexpectedly")
	default:
		t.Fatal("expected the stream end to be reported")
	}
}

// committedStream is fetchStream's flag for a stream whose session Play
// has committed, so its failures are reported asynchronously.
func committedStream() *atomic.Bool {
	committed := new(atomic.Bool)
	committed.Store(true)
	return committed
}

func TestFetchStream_FailureBeforeCommitGoesThroughThePipe(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("audio-bytes"))
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw, new(atomic.Bool))

	// Data flowed, but no session plays it yet: Play (parked in the
	// decoder) owns the failure, so it arrives on the pipe and only there.
	data, err := drainPipe(pr)
	assert.Equal(t, "audio-bytes", string(data))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream ended unexpectedly")
	select {
	case reported := <-p.errChan:
		t.Fatalf("a failure before commit must not also be reported async, got: %v", reported)
	default:
	}
}

func TestFetchStream_StalledStreamReportsError(t *testing.T) {
	securitytest.AllowTestHosts(t)

	// Send some data, then hold the connection open without closing it: the
	// classic silent stall (lost link, NAT timeout) that never errors.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("some-audio"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release) // must run before server.Close, which waits on handlers

	p := newTestPlayer()
	p.stallTimeout = 150 * time.Millisecond
	pr, pw := io.Pipe()

	done := make(chan struct{})
	go func() {
		p.fetchStream(context.Background(), 1, server.URL, pw, committedStream())
		close(done)
	}()

	data, _ := drainPipe(pr)
	<-done

	assert.Equal(t, "some-audio", string(data), "data before the stall must pass through")
	select {
	case reported := <-p.errChan:
		assert.Contains(t, reported.Error(), "stream stalled")
	default:
		t.Fatal("expected the stall to be reported")
	}
}

func TestFetchStream_UnresponsiveServerReportsStall(t *testing.T) {
	securitytest.AllowTestHosts(t)

	// The server never even sends response headers.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release) // must run before server.Close, which waits on handlers

	p := newTestPlayer()
	p.stallTimeout = 150 * time.Millisecond
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw, committedStream())

	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream stalled")

	// A stall before any data flowed is a connect failure: the pipe (and so
	// Play's return value) is its only owner, with no async duplicate.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

// newUndecodableStreamServer streams zeros (no MP3 sync word, no ADTS
// header) for as long as the client keeps reading: data flows the whole
// time, so the stall watchdog stays quiet, but nothing ever decodes. It
// trickles: both decoders give up on a stream with no frame in its first
// 32 KiB, and at this pace that is seconds away. The returned channel is
// signalled when a request arrives.
func newUndecodableStreamServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	securitytest.AllowTestHosts(t)
	arrived := make(chan struct{}, 1)
	chunk := make([]byte, 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		for {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, arrived
}

// playAsync runs Play on its own goroutine and hands back its result.
func playAsync(p *AudioPlayer, url, format string, gen uint64) <-chan error {
	done := make(chan error, 1)
	go func() { done <- p.Play(url, format, gen) }()
	return done
}

// awaitPlay returns the result of a playAsync call, failing the test if
// Play is still blocked after d.
func awaitPlay(t *testing.T, done <-chan error, d time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("Play still blocked after %s", d)
		return nil
	}
}

func TestPlay_ConnectDeadlineFiresBeforeStallWatchdog(t *testing.T) {
	securitytest.AllowTestHosts(t)

	// Headers arrive but no body ever does: the connect deadline, not the
	// (much longer) stall watchdog, must end the attempt.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
	}))
	defer server.Close()
	defer close(release)

	p := newTestPlayer()
	p.stallTimeout = 5 * time.Second
	p.connectTimeout = 100 * time.Millisecond
	start := time.Now()
	err := awaitPlay(t, playAsync(p, server.URL, FormatMP3, testGen.Add(1)), 3*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream connect timed out")
	assert.Less(t, time.Since(start), 2*time.Second)
	// Play returned the failure; an async duplicate could kill a later,
	// healthy session.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

func TestPlay_ConnectDeadlineHoldsUntilAudioDecodes(t *testing.T) {
	server, _ := newUndecodableStreamServer(t)
	p := newTestPlayer()
	p.connectTimeout = 200 * time.Millisecond
	p.prefillWait = 10 * time.Millisecond // the trickle reaches the decoder at once

	// Bytes keep arriving but never decode. A deadline disarmed by the
	// first byte would leave Play parked in the decoder for as long as the
	// server kept sending.
	start := time.Now()
	err := awaitPlay(t, playAsync(p, server.URL, FormatMP3, testGen.Add(1)), 3*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream connect timed out")
	assert.Less(t, time.Since(start), 2*time.Second)
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

func TestPlay_ConnectDeadlineEndsOnceAudioDecodes(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	p.connectTimeout = 300 * time.Millisecond
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	// The server sends its burst and then goes quiet for longer than the
	// connect deadline: once audio decodes, only the stall watchdog applies.
	require.NoError(t, playNext(p, server.URL, FormatMP3))
	time.Sleep(2 * p.connectTimeout)

	select {
	case err := <-p.Errors():
		t.Fatalf("a playing session must outlive the connect deadline, got: %v", err)
	default:
	}
	assert.Zero(t, ctx.pauses.Load(), "the session must still be playing")
}

func TestPlay_FailureBeforeCommitIsOnlyReturned(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// Some bytes arrive, none decode, then the server hangs up: the stream
	// fails after data flowed but before a session committed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 1024))
	}))
	t.Cleanup(server.Close)
	p := newTestPlayer()

	require.Error(t, playNext(p, server.URL, FormatMP3))

	// Play returned the failure. The caller retries the same generation on
	// the next mirror or format, so an async duplicate would fail the
	// stream that then succeeds.
	select {
	case reported := <-p.errChan:
		t.Fatalf("a failure before commit must not also be reported async, got: %v", reported)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPlay_StopCancelsConnectInFlight(t *testing.T) {
	server, arrived := newUndecodableStreamServer(t)
	p := newTestPlayer()

	done := playAsync(p, server.URL, FormatMP3, testGen.Add(1))
	<-arrived
	stopNext(p)

	// The stream never decodes and the connect deadline is still seconds
	// away: only the Stop itself can end Play this soon.
	err := awaitPlay(t, done, time.Second)
	assert.ErrorIs(t, err, ErrSuperseded)
}

func TestPlay_NewerPlayCancelsConnectInFlight(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	junk, arrived := newUndecodableStreamServer(t)
	good := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	older := playAsync(p, junk.URL, FormatMP3, testGen.Add(1))
	<-arrived
	require.NoError(t, playNext(p, good.URL, FormatMP3))

	err := awaitPlay(t, older, time.Second)
	assert.ErrorIs(t, err, ErrSuperseded)
	assert.EqualValues(t, 1, ctx.players.Load(), "only the newer Play may commit")
}

func TestWatchdogReader_RearmsOnData(t *testing.T) {
	var fired atomic.Bool
	timers := newStreamTimers(100*time.Millisecond, func() { fired.Store(true) })
	defer timers.stop()

	pr, pw := io.Pipe()
	w := &watchdogReader{r: pr, timers: timers}

	// Keep data flowing for well past the stall timeout; the watchdog must
	// not fire while reads succeed.
	go func() {
		for i := 0; i < 6; i++ {
			_, _ = pw.Write([]byte("x"))
			time.Sleep(40 * time.Millisecond)
		}
		_ = pw.Close()
	}()

	_, err := io.ReadAll(w)
	require.NoError(t, err)
	assert.False(t, fired.Load(), "watchdog must not fire while data flows")
}

func TestFetchStream_RequestsAndDemuxesICYMetadata(t *testing.T) {
	securitytest.AllowTestHosts(t)

	var gotIcyHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIcyHeader = r.Header.Get("Icy-MetaData")
		b := &icyStreamBuilder{icyInt: 8}
		b.segment(0xAA, "StreamTitle='Demuxed Song';")
		w.Header().Set("icy-metaint", "8")
		_, _ = w.Write(b.buf.Bytes())
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw, committedStream())

	data, err := drainPipe(pr)
	require.NoError(t, err)

	assert.Equal(t, "1", gotIcyHeader, "fetchStream must request ICY metadata")
	assert.Equal(t, bytes.Repeat([]byte{0xAA}, 8), data, "metadata must not reach the decoder")

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "Demuxed Song", info.Title)
		assert.EqualValues(t, 1, info.Gen, "titles carry the session generation")
	default:
		t.Fatal("expected a track update from the demuxed metadata")
	}
}

func TestFetchStream_NoICYHeaderPassesThrough(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No icy-metaint header: the body must be forwarded untouched.
		_, _ = w.Write([]byte("plain-audio"))
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw, committedStream())

	data, err := drainPipe(pr)
	require.NoError(t, err)
	assert.Equal(t, "plain-audio", string(data))
	assert.Empty(t, p.trackChan)
}

func TestSetVolume_ClampsAndStores(t *testing.T) {
	p := newTestPlayer()
	p.volume = 1

	p.SetVolume(0.5)
	assert.InDelta(t, 0.5, p.Volume(), 1e-9)

	p.SetVolume(-0.2)
	assert.Zero(t, p.Volume())

	p.SetVolume(1.7)
	assert.InDelta(t, 1.0, p.Volume(), 1e-9)

	p.SetVolume(math.NaN())
	assert.Zero(t, p.Volume())
	p.SetVolume(math.Inf(1))
	assert.InDelta(t, 1.0, p.Volume(), 1e-9)
	p.SetVolume(math.Inf(-1))
	assert.Zero(t, p.Volume())
}

func TestPerceptualVolume_CubicMapping(t *testing.T) {
	assert.Zero(t, perceptualVolume(0))
	assert.InDelta(t, 1.0, perceptualVolume(1), 1e-9)
	assert.InDelta(t, 0.125, perceptualVolume(0.5), 1e-9)
	assert.InDelta(t, 0.001, perceptualVolume(0.1), 1e-9)
	// The curve concentrates headroom at the low end: half the linear
	// target maps to well under half the amplitude.
	assert.Less(t, perceptualVolume(0.5), 0.5)
}

func TestSetVolume_AppliesPerceptualCurveToActiveSession(t *testing.T) {
	p := newTestPlayer()
	s := &session{volumeCh: make(chan float64, 1)}
	p.current = s

	p.SetVolume(0.5)

	assert.InDelta(t, 0.5, p.Volume(), 1e-9, "Volume() stays the un-curved target")
	select {
	case v := <-s.volumeCh:
		assert.InDelta(t, 0.125, v, 1e-9, "the session receives the curved amplitude")
	default:
		t.Fatal("expected a pending volume target")
	}
}

func TestFadeIn_AppliesPerceptualCurve(t *testing.T) {
	p, _, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	p.SetVolume(0.5)
	require.NoError(t, playNext(p, server.URL, FormatMP3))

	p.mu.Lock()
	s := p.current
	p.mu.Unlock()
	require.NotNil(t, s)

	want := perceptualVolume(0.5)
	require.Eventually(t, func() bool {
		v := s.player.Volume()
		diff := v - want
		if diff < 0 {
			diff = -diff
		}
		return diff < 1e-9
	}, time.Second, 10*time.Millisecond, "fade-in must settle on the curved amplitude, not the linear target")
}

func TestSessionSetVolume_NewestWins(t *testing.T) {
	s := &session{volumeCh: make(chan float64, 1)}

	s.setVolume(0.3)
	s.setVolume(0.7) // replaces the pending 0.3

	select {
	case v := <-s.volumeCh:
		assert.InDelta(t, 0.7, v, 1e-9)
	default:
		t.Fatal("expected a pending volume target")
	}
}

func TestReportTrack_NewestWins(t *testing.T) {
	p := newTestPlayer()

	p.reportTrack(context.Background(), TrackInfo{Title: "First"})
	p.reportTrack(context.Background(), TrackInfo{Title: "Second"})

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "Second", info.Title)
	default:
		t.Fatal("expected a pending track update")
	}
}

func TestReportTrack_OlderGenerationNeverDisplacesNewer(t *testing.T) {
	p := newTestPlayer()
	p.playGen = 2

	p.reportTrack(context.Background(), TrackInfo{Title: "New Channel", Gen: 2})
	// The previous channel, still fading out, reports a title of its own.
	p.reportTrack(context.Background(), TrackInfo{Title: "Old Channel", Gen: 1})

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "New Channel", info.Title)
	default:
		t.Fatal("expected a pending track update")
	}
}

// The new stream's first title usually arrives before Play commits the
// session. It must survive the commit instead of being discarded as a
// leftover of the previous channel, which generations already filter out.
func TestPlay_KeepsTitleReportedBeforeCommit(t *testing.T) {
	p, _, _ := newLifecycleTestPlayer(t)
	securitytest.AllowTestHosts(t)
	// Shorter than one MP3 frame, so the title is demuxed before the
	// decoder has its first frame, i.e. before the session commits.
	const icyInt = 100
	const title = "StreamTitle='First Song';"
	audio := silentMP3Frames(30)
	var body bytes.Buffer
	for i := 0; i < len(audio); i += icyInt {
		body.Write(audio[i:min(i+icyInt, len(audio))])
		if i != 0 {
			body.WriteByte(0) // no metadata change
			continue
		}
		metaLen := (len(title) + 15) / 16
		body.WriteByte(byte(metaLen))
		body.WriteString(title)
		body.Write(make([]byte, metaLen*16-len(title)))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("icy-metaint", strconv.Itoa(icyInt))
		_, _ = w.Write(body.Bytes())
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { stopNext(p) })

	gen := testGen.Add(1)
	require.NoError(t, p.Play(server.URL, FormatMP3, gen))

	select {
	case info := <-p.TrackUpdates():
		assert.Equal(t, "First Song", info.Title)
		assert.Equal(t, gen, info.Gen)
	case <-time.After(2 * time.Second):
		t.Fatal("the stream's first title was lost")
	}
}

func TestReportTrack_CancelledContextDropped(t *testing.T) {
	p := newTestPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p.reportTrack(ctx, TrackInfo{Title: "Stale"})

	assert.Empty(t, p.trackChan, "superseded sessions must not publish titles")
}

func TestFetchStream_InvalidURL(t *testing.T) {
	p := newTestPlayer()
	pr, pw := io.Pipe()

	go p.fetchStream(context.Background(), 1, "http://evil.example.com/stream", pw, committedStream())

	// The pipe reader should observe the error propagated via CloseWithError.
	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid stream URL")

	// The pipe is the failure's only owner: Play returns it synchronously,
	// and an async duplicate could kill a later, healthy session.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

func TestFetchStream_BadStatusCode(t *testing.T) {
	securitytest.AllowTestHosts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := newTestPlayer()
	pr, pw := io.Pipe()
	go p.fetchStream(context.Background(), 1, server.URL, pw, committedStream())

	_, err := drainPipe(pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")

	// Synchronous failures own their error exclusively; see above.
	select {
	case reported := <-p.errChan:
		t.Fatalf("connect failure must not also be reported async, got: %v", reported)
	default:
	}
}

func TestFetchStream_CancelledContextSuppressesReadError(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// Server that blocks so the copy is interrupted by cancellation.
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // hold the connection open until the test releases it
	}))
	defer server.Close()
	defer close(release)

	p := newTestPlayer()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()

	done := make(chan struct{})
	go func() {
		p.fetchStream(ctx, 1, server.URL, pw, committedStream())
		close(done)
	}()

	// Cancel the request, then drain the reader so fetchStream can return.
	cancel()
	_, _ = drainPipe(pr)
	<-done

	// A read error caused by our own cancellation must not be reported.
	select {
	case err := <-p.errChan:
		t.Fatalf("cancellation should not report an error, got: %v", err)
	default:
	}
}

func TestPlay_FetchFailureIsReportedAsOne(t *testing.T) {
	securitytest.AllowTestHosts(t)
	notFound := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notFound.Close)
	// A server that has gone away: nothing listens on its port any more.
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()

	for _, tc := range []struct{ name, url, want string }{
		{"not found", notFound.URL, "unexpected status code: 404"},
		{"connection refused", gone.URL, "failed to fetch stream: "},
		{"host not allowed", "http://evil.example.com/stream", "invalid stream URL: "},
	} {
		for _, format := range PreferredFormats() {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				err := playNext(newTestPlayer(), tc.url, format)
				require.Error(t, err)
				// The decoder is where the failure comes to light, since the
				// fetch fails through the pipe under it, but being offline
				// is not a failure to decode, and the message is what the
				// status line shows.
				assert.True(t, strings.HasPrefix(err.Error(), tc.want), "got: %v", err)
				assert.NotContains(t, err.Error(), "decode")
			})
		}
	}
}

func TestPlay_UndecodableStreamIsReportedAsADecodeFailure(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// The fetch is fine; what it fetches is not audio in any format, and
	// there is more of it than a decoder searches for a frame.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(make([]byte, 2*adtsMaxSkip))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)

	for _, format := range PreferredFormats() {
		t.Run(format, func(t *testing.T) {
			err := awaitPlay(t, playAsync(newTestPlayer(), server.URL, format, testGen.Add(1)), 3*time.Second)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed to decode "+format+" stream: ")
		})
	}
}

func TestPlay_StreamEndingInsideItsFirstFrameIsAFetchFailure(t *testing.T) {
	securitytest.AllowTestHosts(t)
	// The server hangs up a few bytes into the first frame. go-mp3 puts
	// the read error it was given into a message of its own without
	// wrapping it, so its error cannot be asked what went wrong; the pipe
	// can.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(silentMP3Frames(1)[:14])
	}))
	t.Cleanup(server.Close)

	err := playNext(newTestPlayer(), server.URL, FormatMP3)
	assert.EqualError(t, err, "stream ended unexpectedly")
}

func TestBuildPipeline_FetchFailureBehindBufferedAudioIsReportedAsOne(t *testing.T) {
	p := newTestPlayer()
	// The fetch failed just as Play committed: the audio that was buffered
	// decodes, and the failure follows it through the pipe, here in the
	// middle of a frame. It is the stream that failed, not the decoder.
	audio := append(silentMP3Frames(2), silentMP3Frames(1)[:14]...)
	failure := &fetchError{errors.New("stream ended unexpectedly")}
	stream, err := p.buildPipeline(context.Background(), 1, FormatMP3,
		io.MultiReader(bytes.NewReader(audio), iotest.ErrReader(failure)))
	require.NoError(t, err)

	_, err = io.Copy(io.Discard, stream)
	require.Error(t, err)
	select {
	case reported := <-p.errChan:
		assert.EqualError(t, reported, "stream ended unexpectedly")
	default:
		t.Fatal("the failure was not reported")
	}
}

// failingDecoder yields silence for a while, then fails like a decoder that
// hit corrupt data mid-stream.
type failingDecoder struct {
	reads int
	err   error
}

func (d *failingDecoder) Read(b []byte) (int, error) {
	d.reads++
	if d.reads > 3 {
		return 0, d.err
	}
	return len(b), nil
}

func (d *failingDecoder) SampleRate() int { return sampleRate }

func TestPlay_DecoderErrorAfterPlayIsReported(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	ctx.pump = true
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	prevDecoder := newDecoder
	newDecoder = func(_ string, r io.Reader) (pcmDecoder, error) {
		// Drain the pipe on a goroutine so the fetch side never blocks; the
		// decoder's own output does not depend on it.
		go func() { _, _ = io.Copy(io.Discard, r) }()
		return &failingDecoder{err: errors.New("corrupt frame")}, nil
	}
	t.Cleanup(func() { newDecoder = prevDecoder })

	require.NoError(t, playNext(p, server.URL, FormatMP3))

	select {
	case err := <-p.Errors():
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode error")
		assert.Contains(t, err.Error(), "corrupt frame")
	case <-time.After(2 * time.Second):
		t.Fatal("decoder error after Play was never reported")
	}
}

func TestPlay_DecoderErrorAfterStopIsSuppressed(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	// Not pumped: the first read happens only after Stop has cancelled the
	// session, which is when a real teardown closes the pipe under the
	// decoder.
	var reader io.Reader
	prevDecoder := newDecoder
	newDecoder = func(_ string, r io.Reader) (pcmDecoder, error) {
		return &failingDecoder{err: errors.New("closed pipe")}, nil
	}
	t.Cleanup(func() { newDecoder = prevDecoder })
	p.newContext = func() (audioContext, <-chan struct{}, error) {
		ready := make(chan struct{})
		close(ready)
		return &captureContext{fakeAudioContext: ctx, onPlayer: func(r io.Reader) { reader = r }}, ready, nil
	}

	require.NoError(t, playNext(p, server.URL, FormatMP3))
	stopNext(p)
	require.Eventually(t, func() bool { return ctx.pauses.Load() == 1 }, time.Second, 10*time.Millisecond)

	buf := make([]byte, 16)
	for i := 0; i < 5; i++ {
		_, _ = reader.Read(buf)
	}
	select {
	case err := <-p.Errors():
		t.Fatalf("error from a stopped session must be dropped, got %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

// captureContext hands the reader given to NewPlayer to a callback.
type captureContext struct {
	*fakeAudioContext
	onPlayer func(io.Reader)
}

func (c *captureContext) NewPlayer(r io.Reader) outputPlayer {
	c.onPlayer(r)
	return c.fakeAudioContext.NewPlayer(r)
}

func TestPlay_StaleGenerationIsRefused(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	newer := testGen.Add(2)
	require.NoError(t, p.Play(server.URL, FormatMP3, newer))
	// A request issued before the one that just committed arrives late: it
	// must not touch the audio state.
	require.ErrorIs(t, p.Play(server.URL, FormatMP3, newer-1), ErrSuperseded)
	assert.EqualValues(t, 1, ctx.players.Load())
	// Retrying the same generation (a stream-candidate fallback) is allowed.
	require.NoError(t, p.Play(server.URL, FormatMP3, newer))
	assert.EqualValues(t, 2, ctx.players.Load())
}

func TestStop_StaleGenerationIsIgnored(t *testing.T) {
	p, ctx, _ := newLifecycleTestPlayer(t)
	server := newStreamingTestServer(t)
	t.Cleanup(func() { stopNext(p) })

	gen := testGen.Add(2)
	require.NoError(t, p.Play(server.URL, FormatMP3, gen))
	p.Stop(gen - 1)
	p.mu.Lock()
	current := p.current
	p.mu.Unlock()
	assert.NotNil(t, current, "a stale stop must not tear down a newer session")
	assert.Zero(t, ctx.pauses.Load())

	// The session's own generation stops it (the server reacting to its
	// stream error).
	p.Stop(gen)
	require.Eventually(t, func() bool { return ctx.pauses.Load() == 1 }, time.Second, 10*time.Millisecond)
}

func TestReportError_OlderGenerationIsDropped(t *testing.T) {
	p := newTestPlayer()
	p.playGen = 2

	// The previous channel, still fading out under its successor (its
	// context is cancelled only once the fade ends), loses its stream.
	p.reportError(context.Background(), 1, errors.New("old stream ended"))
	p.reportError(context.Background(), 1, errors.New("old decoder failed"))
	assert.Empty(t, p.errChan, "a superseded session must not take up the error slots")

	p.reportError(context.Background(), 2, errors.New("new stream failed"))
	select {
	case err := <-p.errChan:
		assert.EqualError(t, err, "new stream failed")
	default:
		t.Fatal("the current session's error was not delivered")
	}
}

func TestReportError_CarriesGeneration(t *testing.T) {
	p := newTestPlayer()
	p.reportError(context.Background(), 7, errors.New("boom"))
	err := <-p.errChan
	var se *StreamError
	require.ErrorAs(t, err, &se)
	assert.EqualValues(t, 7, se.Gen)
	assert.EqualError(t, err, "boom")
}
