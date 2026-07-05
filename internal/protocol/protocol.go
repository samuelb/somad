// Package protocol defines the wire protocol between the somatui client and
// server: newline-delimited JSON over a Unix domain socket. Clients send
// Requests; the server answers with Responses (correlated by ID) and pushes
// Events carrying full state snapshots.
package protocol

import "encoding/json"

// Version is the protocol version. A client and server must agree on it
// exactly; bump it on any incompatible wire change.
const Version = 1

// Method names for Request.Method.
const (
	MethodHello          = "hello"
	MethodStatus         = "status"
	MethodChannels       = "channels"
	MethodPlay           = "play"
	MethodPlayPause      = "playPause"
	MethodPlayRelative   = "playRelative"
	MethodStop           = "stop"
	MethodSetVolume      = "setVolume"
	MethodToggleFavorite = "toggleFavorite"
	MethodShutdown       = "shutdown"
)

// Event names for Event.Event.
const (
	EventState    = "state"
	EventChannels = "channels"
)

// Request is a client-to-server call.
type Request struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response answers the Request with the same ID. Exactly one of Error and
// Result is meaningful.
type Response struct {
	ID     int64           `json:"id"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Event is a server-initiated push. It carries no ID.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// NewEvent builds an Event with data marshaled into the payload.
func NewEvent(name string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	return Event{Event: name, Data: raw}, nil
}

// ServerMessage is the union a client decodes each server line into: a line
// with an ID is a Response, a line with an Event name is an Event.
type ServerMessage struct {
	ID     *int64          `json:"id,omitempty"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Event  string          `json:"event,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}
