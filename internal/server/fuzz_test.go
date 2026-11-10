package server

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// FuzzServeConn feeds a connection whatever a peer might send. The request
// path is the daemon's one input from outside: on a TCP listener without a
// PSK every line of it comes from an unauthenticated peer, and the lines
// before auth do with one. Nothing a peer sends may panic the daemon, and
// the connection must end once its input does, whatever was asked for on
// the way (a stream, a sleep timer, a spectrum subscription, a shutdown).
func FuzzServeConn(f *testing.F) {
	const hello = `{"id":1,"method":"hello","params":{"clientVersion":"fuzz","protocolVersion":2}}`
	session := func(requests ...string) []byte {
		return []byte(strings.Join(append([]string{hello}, requests...), "\n") + "\n")
	}
	f.Add(session(
		`{"id":2,"method":"status"}`,
		`{"id":3,"method":"channels"}`,
		`{"id":4,"method":"play","params":{"channelId":"dronezone"}}`,
		`{"id":5,"method":"setVolume","params":{"volume":0.5}}`,
		`{"id":6,"method":"toggleMute"}`,
		`{"id":7,"method":"playRelative","params":{"delta":-1}}`,
		`{"id":8,"method":"stop","params":{"in":"45m"}}`,
		`{"id":9,"method":"stop","params":{"cancel":true}}`,
		`{"id":10,"method":"playPause"}`,
		`{"id":11,"method":"stop"}`,
	))
	f.Add(session(
		`{"id":2,"method":"toggleFavorite","params":{"channelId":"groovesalad"}}`,
		`{"id":3,"method":"history","params":{"channelId":"groovesalad","limit":5}}`,
		`{"id":4,"method":"history","params":{"limit":-1}}`,
		`{"id":5,"method":"spectrum","params":{"bands":24}}`,
		`{"id":6,"method":"spectrum","params":{"bands":0}}`,
		`{"id":7,"method":"reloadLastfm"}`,
		`{"id":8,"method":"shutdown"}`,
	))
	// Before hello, and around the pre-shared-key exchange.
	f.Add([]byte(`{"id":1,"method":"status"}` + "\n"))
	f.Add([]byte(`{"id":1,"method":"authChallenge"}` + "\n" + `{"id":2,"method":"auth","params":{"mac":"AAAA"}}` + "\n"))
	f.Add([]byte(`{"id":1,"method":"hello","params":{"clientVersion":"fuzz","protocolVersion":1}}` + "\n"))
	// Not a client at all.
	f.Add([]byte("POST / HTTP/1.1\r\nHost: 127.0.0.1:5454\r\nContent-Type: text/plain\r\n\r\n" + hello + "\n"))
	f.Add(session(`{"id":2,"method":"setVolume","params":{"volume":"loud"}}`, `{"id":3,"method":"nope"}`, `[]`))
	f.Add([]byte(""))

	// Once for every input: requests are handled on goroutines of their
	// own, which can outlive the connection, and so the input's run.
	isolateServer(f)

	f.Fuzz(func(t *testing.T, input []byte) {
		s, _ := startTestServer(t, Config{})
		client, server := net.Pipe()
		served := make(chan struct{})
		go func() {
			defer close(served)
			s.serveConn(server)
		}()
		// The pipe has no buffer: read what the server answers, or its
		// writes would stall it.
		go func() { _, _ = io.Copy(io.Discard, client) }()

		// A write error means the server hung up early, as it does at a
		// line that is not a request.
		_, _ = client.Write(input)
		_ = client.Close()

		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Fatal("the connection outlived its input")
		}
	})
}
