package server

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotifyPipeline_SendsQueuedPayload(t *testing.T) {
	sent := make(chan notifyPayload, 1)
	p := newNotifyPipeline(func(title, body string) {
		sent <- notifyPayload{title: title, body: body}
	})

	p.queue("Title", "Body")

	select {
	case got := <-sent:
		assert.Equal(t, notifyPayload{title: "Title", body: "Body"}, got)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the queued payload to be sent")
	}
}

// TestNotifyPipeline_CoalescesBurstIntoLatest exercises the "latest-wins"
// requirement: while a send is in flight, further queue calls must not each
// spawn their own sender, and once the in-flight send finishes only the
// newest payload is sent, not every intermediate one.
func TestNotifyPipeline_CoalescesBurstIntoLatest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var sendCount int
	var lastTitle string
	p := newNotifyPipeline(func(title, _ string) {
		mu.Lock()
		sendCount++
		lastTitle = title
		mu.Unlock()
		select {
		case started <- struct{}{}:
		default:
		}
		<-release // block the in-flight send until the test releases it
	})

	p.queue("First", "")
	<-started // the first send is now blocked inside the notifier

	// Arrive while the first send is still in flight: must coalesce into one
	// pending payload rather than each starting a sender of their own.
	p.queue("Second", "")
	p.queue("Third", "")

	close(release) // let the first send return; drain() picks up "Third" next

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return sendCount == 2 && lastTitle == "Third"
	}, 2*time.Second, 10*time.Millisecond, "expected exactly one coalesced send of the latest title")
}
