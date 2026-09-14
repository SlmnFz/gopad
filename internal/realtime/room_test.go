package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/local/gopad/internal/crdt"
	"github.com/local/gopad/internal/store"
)

const testDocumentSlug = "Abc123456789"

type testStore struct {
	mu       sync.Mutex
	document store.LoadedDocument
	appends  []crdt.Operation
}

func newTestStore() *testStore {
	return &testStore{
		document: store.LoadedDocument{Document: store.Document{
			ID:       7,
			Slug:     testDocumentSlug,
			Snapshot: []crdt.Char{},
		}},
	}
}

func (s *testStore) CreateDocument(context.Context) (store.Document, error) {
	return s.document.Document, nil
}

func (s *testStore) LoadDocument(_ context.Context, slug string) (store.LoadedDocument, error) {
	if slug != s.document.Slug {
		return store.LoadedDocument{}, store.ErrDocumentNotFound
	}
	return s.document, nil
}

func (s *testStore) AppendOperations(_ context.Context, _ int64, operations []crdt.Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appends = append(s.appends, operations...)
	return nil
}

func TestHub_GetOrCreateRoomIsIdempotentAndEvictsIdleRooms(t *testing.T) {
	hub := NewHubWithConfig(newTestStore(), HubConfig{
		IdleTimeout:     25 * time.Millisecond,
		ClientQueueSize: 4,
		FanoutQueueSize: 4,
		FanoutWorkers:   1,
	})
	defer hub.Close()

	first, err := hub.GetOrCreateRoom(context.Background(), testDocumentSlug)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.GetOrCreateRoom(context.Background(), testDocumentSlug)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("GetOrCreateRoom returned different room instances for one slug")
	}

	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("room did not evict after its idle timeout")
	}
	if _, ok := hub.rooms.Load(testDocumentSlug); ok {
		t.Fatal("evicted room is still registered in the hub")
	}
}

func TestRoom_AssignsSequencesAndBroadcastsFIFOWithoutEcho(t *testing.T) {
	hub := NewHubWithConfig(newTestStore(), HubConfig{
		IdleTimeout:     time.Hour,
		ClientQueueSize: 8,
		FanoutQueueSize: 8,
		FanoutWorkers:   1,
	})
	defer hub.Close()

	room, err := hub.GetOrCreateRoom(context.Background(), testDocumentSlug)
	if err != nil {
		t.Fatal(err)
	}
	sender := newTestClient(8)
	receiver := newTestClient(8)
	room.Register(sender)
	room.Register(receiver)
	assertSync(t, readTestEnvelope(t, sender))
	assertSync(t, readTestEnvelope(t, receiver))

	firstID := crdt.CharID{SiteID: "sender", Counter: 1}
	secondID := crdt.CharID{SiteID: "sender", Counter: 2}
	first := crdt.Operation{Type: crdt.Insert, ID: firstID, Value: 'A'}
	second := crdt.Operation{Type: crdt.Insert, ID: secondID, Value: 'B', LeftID: &firstID}
	room.SubmitOperation(sender, first)
	room.SubmitOperation(sender, second)

	firstPayload := decodeOperationPayload(t, readTestEnvelope(t, receiver))
	secondPayload := decodeOperationPayload(t, readTestEnvelope(t, receiver))
	if firstPayload.Sequence != 1 || secondPayload.Sequence != 2 {
		t.Fatalf("sequences = %d, %d; want 1, 2", firstPayload.Sequence, secondPayload.Sequence)
	}
	if firstPayload.Operation.ID != firstID || secondPayload.Operation.ID != secondID {
		t.Fatalf("operations arrived out of order: %#v then %#v", firstPayload.Operation, secondPayload.Operation)
	}
	select {
	case data := <-sender.send:
		t.Fatalf("sender received an unexpected broadcast: %s", data)
	default:
	}
}

func TestRoom_RemovesSlowClientWithoutStallingSender(t *testing.T) {
	hub := NewHubWithConfig(newTestStore(), HubConfig{
		IdleTimeout:     time.Hour,
		ClientQueueSize: 1,
		FanoutQueueSize: 2,
		FanoutWorkers:   1,
	})
	defer hub.Close()

	room, err := hub.GetOrCreateRoom(context.Background(), testDocumentSlug)
	if err != nil {
		t.Fatal(err)
	}
	slow := newTestClient(1)
	sender := newTestClient(1)
	room.Register(slow)
	room.Register(sender)
	readTestEnvelope(t, sender)

	room.SubmitOperation(sender, crdt.Operation{
		Type:  crdt.Insert,
		ID:    crdt.CharID{SiteID: "sender", Counter: 1},
		Value: 'A',
	})
	select {
	case <-slow.done:
	case <-time.After(time.Second):
		t.Fatal("slow client was not removed after its queue filled")
	}
}

func assertSync(t *testing.T, envelope Envelope) {
	t.Helper()
	if envelope.Type != MessageSync {
		t.Fatalf("message type = %q, want %q", envelope.Type, MessageSync)
	}
}

func decodeOperationPayload(t *testing.T, envelope Envelope) OperationPayload {
	t.Helper()
	if envelope.Type != MessageOp {
		t.Fatalf("message type = %q, want %q", envelope.Type, MessageOp)
	}
	var payload OperationPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode operation payload: %v", err)
	}
	return payload
}

func readTestEnvelope(t *testing.T, client *Client) Envelope {
	t.Helper()
	select {
	case data := <-client.send:
		var envelope Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			t.Fatalf("decode envelope: %v", err)
		}
		return envelope
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for room message")
		return Envelope{}
	}
}
