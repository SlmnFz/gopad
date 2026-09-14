package realtime

import (
	"encoding/json"

	"github.com/local/gopad/internal/crdt"
)

const (
	MessageOp       = "op"
	MessageCursor   = "cursor"
	MessagePresence = "presence"
	MessageSync     = "sync"
	MessageError    = "error"
)

// Envelope is the common WebSocket message wrapper.
type Envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// OperationPayload carries a CRDT operation and its server-assigned sequence.
type OperationPayload struct {
	crdt.Operation
	Sequence int64 `json:"sequence,omitempty"`
}

// SyncPayload is sent once to a client when it joins a room.
type SyncPayload struct {
	SiteID   string             `json:"siteID"`
	Snapshot []crdt.Char        `json:"snapshot"`
	Sequence int64              `json:"sequence"`
	Presence []PresenceIdentity `json:"presence"`
}

// PresenceIdentity is reserved for the active-user list populated by Task 7.
type PresenceIdentity struct {
	UserID   int64  `json:"userID"`
	Username string `json:"username"`
	Color    string `json:"color"`
}

// ErrorPayload reports a rejected client operation without closing the room.
type ErrorPayload struct {
	Message string `json:"message"`
}

func marshalEnvelope(messageType string, payload any) ([]byte, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{Type: messageType, Payload: payloadJSON})
}
