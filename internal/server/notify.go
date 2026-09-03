package server

import "sync"

// notifyPipeline coalesces desktop-notification requests so a fast stream of
// track-title changes never piles up goroutines or notifications: while a
// send is in flight (blocking on D-Bus, or forking osascript), a newer
// request just replaces the not-yet-sent one instead of queuing a second
// sender, so only the latest title is ever shown once a sender is free.
type notifyPipeline struct {
	send func(title, body string)

	mu      sync.Mutex
	pending *notifyPayload
	running bool
}

type notifyPayload struct {
	title, body string
}

// newNotifyPipeline returns a pipeline that hands every queued payload to
// send, off the caller's goroutine.
func newNotifyPipeline(send func(title, body string)) *notifyPipeline {
	return &notifyPipeline{send: send}
}

// queue schedules title/body to be shown, replacing any not-yet-sent
// request. It never blocks on send, so it is safe to call while holding
// another lock (handleTrackUpdate calls it under s.mu).
func (p *notifyPipeline) queue(title, body string) {
	p.mu.Lock()
	p.pending = &notifyPayload{title: title, body: body}
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.mu.Unlock()
	go p.drain()
}

// drain sends the latest pending payload, and keeps sending as long as a
// newer one arrived while it was busy, so a burst of queue calls coalesces
// into the fewest possible sends instead of queuing one goroutine each.
func (p *notifyPipeline) drain() {
	for {
		p.mu.Lock()
		payload := p.pending
		p.pending = nil
		if payload == nil {
			p.running = false
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		p.send(payload.title, payload.body)
	}
}
