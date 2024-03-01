package ws

import "encoding/json"

// Message holds the data sent over the websocket.
// The Type field differentiates the actual payload.
type Message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}
