package realtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/local/gopad/internal/crdt"
)

const (
	maxWebSocketMessageSize = 16 << 10
	writeTimeout            = 5 * time.Second
	pingInterval            = 20 * time.Second
	pingTimeout             = 10 * time.Second
)

// Client represents one browser connection and its bounded outbound queue.
type Client struct {
	conn   *websocket.Conn
	send   chan []byte
	done   chan struct{}
	room   *Room
	siteID string

	closeOnce sync.Once
}

func newWebSocketClient(connection *websocket.Conn, queueSize int) *Client {
	return &Client{
		conn:   connection,
		send:   make(chan []byte, queueSize),
		done:   make(chan struct{}),
		siteID: newSiteID(),
	}
}

func newTestClient(queueSize int) *Client {
	return &Client{
		send:   make(chan []byte, queueSize),
		done:   make(chan struct{}),
		siteID: newSiteID(),
	}
}

func (c *Client) enqueue(data []byte) bool {
	if c == nil {
		return false
	}
	copyOfData := append([]byte(nil), data...)
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.send <- copyOfData:
		return true
	case <-c.done:
		return false
	default:
		return false
	}
}

func (c *Client) close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.done)
		if c.conn != nil {
			_ = c.conn.Close(websocket.StatusNormalClosure, "room closed")
		}
	})
}

func (c *Client) serve(ctx context.Context, room *Room) {
	room.Register(c)
	if c.conn == nil {
		return
	}
	go c.writeLoop(ctx)
	go c.pingLoop(ctx)
	c.readLoop(ctx, room)
	c.close()
	room.Unregister(c)
}

func (c *Client) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case data := <-c.send:
			writeContext, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.conn.Write(writeContext, websocket.MessageText, data)
			cancel()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *Client) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.done:
			return
		case <-ticker.C:
			pingContext, cancel := context.WithTimeout(context.Background(), pingTimeout)
			err := c.conn.Ping(pingContext)
			cancel()
			if err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *Client) readLoop(ctx context.Context, room *Room) {
	c.conn.SetReadLimit(maxWebSocketMessageSize)
	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		var envelope Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			c.sendError("invalid message")
			continue
		}
		if envelope.Type != MessageOp {
			continue
		}
		var payload OperationPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			c.sendError("invalid operation")
			continue
		}
		room.SubmitOperation(c, payload.Operation)
	}
}

func (c *Client) sendError(message string) {
	data, err := marshalEnvelope(MessageError, ErrorPayload{Message: message})
	if err == nil {
		_ = c.enqueue(data)
	}
}

func newSiteID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "site-fallback"
	}
	return hex.EncodeToString(bytes[:])
}

func decodeOperation(data []byte) (crdt.Operation, error) {
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return crdt.Operation{}, err
	}
	if envelope.Type != MessageOp {
		return crdt.Operation{}, errors.New("message is not an operation")
	}
	var payload OperationPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return crdt.Operation{}, err
	}
	return payload.Operation, nil
}
