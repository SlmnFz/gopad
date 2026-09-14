package realtime

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/local/gopad/internal/crdt"
	"github.com/local/gopad/internal/server"
)

func TestWebSocket_SecondClientReceivesSyncAndBroadcast(t *testing.T) {
	database := newTestStore()
	hub := NewHubWithConfig(database, HubConfig{
		IdleTimeout:     time.Hour,
		ClientQueueSize: 8,
		FanoutQueueSize: 8,
		FanoutWorkers:   1,
	})
	defer hub.Close()

	httpServer := httptest.NewServer(server.New(database, hub))
	defer httpServer.Close()
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/ws/" + testDocumentSlug

	first, _, err := websocket.Dial(context.Background(), websocketURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close(websocket.StatusNormalClosure, "test complete")
	assertSync(t, readWebSocketEnvelope(t, first))

	second, _, err := websocket.Dial(context.Background(), websocketURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(websocket.StatusNormalClosure, "test complete")
	assertSync(t, readWebSocketEnvelope(t, second))

	operation := crdt.Operation{
		Type:  crdt.Insert,
		ID:    crdt.CharID{SiteID: "browser", Counter: 1},
		Value: 'G',
	}
	message, err := marshalEnvelope(MessageOp, OperationPayload{Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	writeContext, cancel := context.WithTimeout(context.Background(), time.Second)
	err = first.Write(writeContext, websocket.MessageText, message)
	cancel()
	if err != nil {
		t.Fatal(err)
	}

	payload := decodeOperationPayload(t, readWebSocketEnvelope(t, second))
	if payload.Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", payload.Sequence)
	}
	if payload.Operation != operation {
		t.Fatalf("operation = %#v, want %#v", payload.Operation, operation)
	}
}

func readWebSocketEnvelope(t *testing.T, connection *websocket.Conn) Envelope {
	t.Helper()
	readContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := connection.Read(readContext)
	if err != nil {
		t.Fatal(err)
	}
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode WebSocket envelope: %v", err)
	}
	return envelope
}
