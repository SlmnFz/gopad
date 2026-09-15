package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/local/gopad/internal/cache"
	"github.com/local/gopad/internal/crdt"
	"github.com/local/gopad/internal/store"
)

const testDocumentSlug = "Abc123456789"

type testStore struct {
	mu       sync.Mutex
	document store.LoadedDocument
	appends  []crdt.Operation
	loads    int
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

func (s *testStore) FindOrCreateUser(_ context.Context, username string) (store.User, error) {
	if username == "" {
		username = "Test"
	}
	return store.User{ID: 1, Username: username, Color: "#4dd8c0"}, nil
}

func (s *testStore) LoadDocument(_ context.Context, slug string) (store.LoadedDocument, error) {
	s.mu.Lock()
	s.loads++
	s.mu.Unlock()
	if slug != s.document.Slug {
		return store.LoadedDocument{}, store.ErrDocumentNotFound
	}
	return s.document, nil
}

func (s *testStore) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
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

func TestHub_CachesMissingSlugs(t *testing.T) {
	database := newTestStore()
	slugCache := cache.NewSlugCache(time.Minute, time.Minute, 8)
	hub := NewHubWithConfig(database, HubConfig{
		IdleTimeout:     time.Hour,
		ClientQueueSize: 4,
		FanoutQueueSize: 4,
		FanoutWorkers:   1,
		SlugCache:       slugCache,
	})
	defer hub.Close()

	for range 2 {
		if _, err := hub.GetOrCreateRoom(context.Background(), "Zzz123456789"); !errors.Is(err, store.ErrDocumentNotFound) {
			t.Fatalf("GetOrCreateRoom error = %v, want document not found", err)
		}
	}
	if database.loadCount() != 1 {
		t.Fatalf("missing slug loads = %d, want one cached lookup", database.loadCount())
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
	assertPresence(t, readTestEnvelope(t, sender))

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

func TestRoom_PresenceJoinLeaveSnapshotsDoNotPersist(t *testing.T) {
	database := newTestStore()
	hub := NewHubWithConfig(database, HubConfig{
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
	alice := identifiedTestClient(1, "Alice", "#4dd8c0")
	bob := identifiedTestClient(2, "Bob", "#7aa2f7")
	room.Register(alice)
	assertSync(t, readTestEnvelope(t, alice))
	room.Register(bob)

	secondSync := decodeSyncPayload(t, readTestEnvelope(t, bob))
	if len(secondSync.Presence) != 2 {
		t.Fatalf("sync presence count = %d, want 2", len(secondSync.Presence))
	}
	join := decodePresencePayload(t, readTestEnvelope(t, alice))
	if join.Event != "join" || len(join.Active) != 2 || join.User.Username != "Bob" {
		t.Fatalf("join payload = %#v, want Bob and two active users", join)
	}

	room.Unregister(bob)
	leave := decodePresencePayload(t, readTestEnvelope(t, alice))
	if leave.Event != "leave" || len(leave.Active) != 1 || leave.User.Username != "Bob" {
		t.Fatalf("leave payload = %#v, want Bob and one active user", leave)
	}
	database.mu.Lock()
	defer database.mu.Unlock()
	if len(database.appends) != 0 {
		t.Fatalf("presence changed persisted operations: %#v", database.appends)
	}
}

func TestRoom_CursorUpdatesCoalesceBeforeDispatch(t *testing.T) {
	database := newTestStore()
	hub := NewHubWithConfig(database, HubConfig{
		IdleTimeout:     time.Hour,
		ClientQueueSize: 8,
		FanoutQueueSize: 8,
		FanoutWorkers:   1,
	})
	defer hub.Close()

	loaded := database.document
	document := crdt.New()
	room := newRoom(hub, testDocumentSlug, loaded.ID, document, 0, nil)
	client := identifiedTestClient(1, "Alice", "#4dd8c0")
	first := CursorPayload{Start: CursorAnchor{}, End: CursorAnchor{}}
	latest := CursorPayload{
		Start: CursorAnchor{LeftID: &crdt.CharID{SiteID: "site", Counter: 2}},
		End:   CursorAnchor{LeftID: &crdt.CharID{SiteID: "site", Counter: 2}},
	}
	room.SubmitCursor(client, first)
	room.SubmitCursor(client, latest)

	room.cursorMu.Lock()
	pending := room.pendingCursors[client]
	queued := room.cursorQueued[client]
	room.cursorMu.Unlock()
	if !queued || len(room.commands) != 1 {
		t.Fatalf("cursor command was queued more than once: queued=%t commands=%d", queued, len(room.commands))
	}
	if pending.Start.LeftID == nil || *pending.Start.LeftID != *latest.Start.LeftID {
		t.Fatalf("pending cursor = %#v, want latest update %#v", pending, latest)
	}

	go room.run(hub.ctx)
	room.shutdown()
	select {
	case <-room.Done():
	case <-time.After(time.Second):
		t.Fatal("test room did not shut down")
	}
}

func identifiedTestClient(userID int64, username, color string) *Client {
	client := newTestClient(8)
	client.userID = userID
	client.username = username
	client.color = color
	return client
}

func assertSync(t *testing.T, envelope Envelope) {
	t.Helper()
	if envelope.Type != MessageSync {
		t.Fatalf("message type = %q, want %q", envelope.Type, MessageSync)
	}
}

func assertPresence(t *testing.T, envelope Envelope) {
	t.Helper()
	if envelope.Type != MessagePresence {
		t.Fatalf("message type = %q, want %q", envelope.Type, MessagePresence)
	}
}

func decodeSyncPayload(t *testing.T, envelope Envelope) SyncPayload {
	t.Helper()
	assertSync(t, envelope)
	var payload SyncPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode sync payload: %v", err)
	}
	return payload
}

func decodePresencePayload(t *testing.T, envelope Envelope) PresencePayload {
	t.Helper()
	if envelope.Type != MessagePresence {
		t.Fatalf("message type = %q, want %q", envelope.Type, MessagePresence)
	}
	var payload PresencePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decode presence payload: %v", err)
	}
	return payload
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
