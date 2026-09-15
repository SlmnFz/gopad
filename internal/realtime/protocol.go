package realtime

import (
	"encoding/json"

	"github.com/local/gopad/internal/crdt"
)

const (
	MessageOp       = "op"
	MessageHello    = "hello"
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
	UserID   int64              `json:"userID"`
	Username string             `json:"username"`
	Color    string             `json:"color"`
	Snapshot []crdt.Char        `json:"snapshot"`
	Sequence int64              `json:"sequence"`
	Presence []PresenceIdentity `json:"presence"`
}

// HelloPayload is the first message sent by a browser connection.
type HelloPayload struct {
	Username string `json:"username"`
}

// PresenceIdentity identifies one active browser replica.
type PresenceIdentity struct {
	UserID   int64  `json:"userID"`
	Username string `json:"username"`
	Color    string `json:"color"`
	SiteID   string `json:"siteID"`
}

// PresencePayload carries join/leave snapshots without persistence.
type PresencePayload struct {
	Event  string             `json:"event"`
	User   PresenceIdentity   `json:"user"`
	Active []PresenceIdentity `json:"active,omitempty"`
}

// CursorAnchor keeps a caret attached to adjacent CRDT characters as text
// changes around it.
type CursorAnchor struct {
	LeftID  *crdt.CharID `json:"leftID,omitempty"`
	RightID *crdt.CharID `json:"rightID,omitempty"`
}

// CursorPayload is ephemeral and intentionally never reaches the store.
type CursorPayload struct {
	SiteID   string       `json:"siteID"`
	UserID   int64        `json:"userID"`
	Username string       `json:"username"`
	Color    string       `json:"color"`
	Start    CursorAnchor `json:"start"`
	End      CursorAnchor `json:"end"`
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
