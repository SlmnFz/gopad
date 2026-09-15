package realtime

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/local/gopad/internal/crdt"
	"github.com/local/gopad/internal/store"
)

type roomCommandKind uint8

const (
	commandRegister roomCommandKind = iota
	commandUnregister
	commandOperation
	commandCursor
	commandShutdown
)

type roomCommand struct {
	kind         roomCommandKind
	client       *Client
	operation    crdt.Operation
	clientSentAt int64
	cursor       CursorPayload
}

// Room serializes all canonical CRDT mutation through one owner goroutine.
type Room struct {
	hub              *Hub
	slug             string
	documentID       int64
	document         *crdt.Document
	sequence         int64
	operations       []store.OperationRecord
	persistTail      <-chan struct{}
	opsSinceSnapshot int

	commands chan roomCommand
	done     chan struct{}
	clients  map[*Client]struct{}

	cursorMu       sync.Mutex
	pendingCursors map[*Client]CursorPayload
	cursorQueued   map[*Client]bool

	shutdownOnce sync.Once
}

func newRoom(hub *Hub, slug string, documentID int64, document *crdt.Document, snapshotVersion int64, operations []store.OperationRecord) *Room {
	sequence := snapshotVersion
	for _, record := range operations {
		if record.Sequence > sequence {
			sequence = record.Sequence
		}
	}
	persisted := make(chan struct{})
	close(persisted)
	return &Room{
		hub:            hub,
		slug:           slug,
		documentID:     documentID,
		document:       document,
		sequence:       sequence,
		operations:     operations,
		persistTail:    persisted,
		commands:       make(chan roomCommand, hub.config.ClientQueueSize),
		done:           make(chan struct{}),
		clients:        make(map[*Client]struct{}),
		pendingCursors: make(map[*Client]CursorPayload),
		cursorQueued:   make(map[*Client]bool),
	}
}

func documentFromLoaded(loaded store.LoadedDocument) (*crdt.Document, error) {
	document := crdt.New()
	for _, char := range loaded.Snapshot {
		if err := document.Apply(crdt.Operation{
			Type:    crdt.Insert,
			ID:      char.ID,
			Value:   char.Value,
			LeftID:  char.LeftID,
			RightID: char.RightID,
		}); err != nil {
			return nil, fmt.Errorf("apply snapshot insert: %w", err)
		}
		if char.Deleted {
			if err := document.Apply(crdt.Operation{Type: crdt.Delete, ID: char.ID}); err != nil {
				return nil, fmt.Errorf("apply snapshot tombstone: %w", err)
			}
		}
	}
	for _, record := range loaded.Operations {
		if err := document.Apply(record.Operation); err != nil {
			return nil, fmt.Errorf("replay operation %d: %w", record.Sequence, err)
		}
	}
	return document, nil
}

func (r *Room) run(ctx context.Context) {
	var idleTimer *time.Timer
	var idleC <-chan time.Time
	defer close(r.done)

	stopIdleTimer := func() {
		if idleTimer == nil {
			return
		}
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer = nil
		idleC = nil
	}
	startIdleTimer := func() {
		stopIdleTimer()
		idleTimer = time.NewTimer(r.hub.config.IdleTimeout)
		idleC = idleTimer.C
	}
	startIdleTimer()
	var snapshotTicker *time.Ticker
	var snapshotC <-chan time.Time
	if r.hub.writer != nil {
		snapshotTicker = time.NewTicker(r.hub.config.SnapshotInterval)
		snapshotC = snapshotTicker.C
		defer snapshotTicker.Stop()
	}

	for {
		select {
		case command := <-r.commands:
			switch command.kind {
			case commandRegister:
				stopIdleTimer()
				r.register(command.client)
			case commandUnregister:
				r.unregister(command.client)
				if len(r.clients) == 0 {
					startIdleTimer()
				}
			case commandOperation:
				r.applyOperation(ctx, command.client, command.operation, command.clientSentAt)
			case commandCursor:
				r.applyCursor(command.client)
			case commandShutdown:
				r.requestSnapshot()
				r.closeClients()
				return
			}
		case <-idleC:
			if len(r.clients) == 0 {
				r.requestSnapshot()
				r.hub.removeRoom(r)
				return
			}
			idleC = nil
			idleTimer = nil
		case <-ctx.Done():
			r.requestSnapshot()
			r.closeClients()
			return
		case <-snapshotC:
			r.requestSnapshot()
		}
	}
}

func (r *Room) register(client *Client) {
	if client == nil {
		return
	}
	client.room = r
	r.clients[client] = struct{}{}
	payload := SyncPayload{
		SiteID:   client.siteID,
		UserID:   client.userID,
		Username: client.username,
		Color:    client.color,
		Snapshot: r.document.Snapshot(),
		Sequence: r.sequence,
		Presence: r.activePresence(),
	}
	data, err := marshalEnvelope(MessageSync, payload)
	if err != nil || !client.enqueue(data) {
		delete(r.clients, client)
		client.close()
		return
	}
	r.hub.metrics.IncActiveConnections()
	r.broadcastPresence("join", client, client)
}

func (r *Room) unregister(client *Client) {
	if client == nil {
		return
	}
	if _, ok := r.clients[client]; ok {
		delete(r.clients, client)
		r.hub.metrics.DecActiveConnections()
		r.clearCursor(client)
		client.close()
		r.broadcastPresence("leave", client, nil)
	}
}

func (r *Room) activePresence() []PresenceIdentity {
	active := make([]PresenceIdentity, 0, len(r.clients))
	for client := range r.clients {
		active = append(active, client.identity())
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].SiteID < active[j].SiteID
	})
	return active
}

func (r *Room) broadcastPresence(event string, client *Client, exclude *Client) {
	payload := PresencePayload{Event: event, User: client.identity(), Active: r.activePresence()}
	data, err := marshalEnvelope(MessagePresence, payload)
	if err != nil {
		return
	}
	r.broadcast(data, exclude)
}

func (r *Room) broadcast(data []byte, exclude *Client) {
	start := time.Now()
	for client := range r.clients {
		if client == exclude {
			continue
		}
		job := fanoutJob{
			client: client,
			data:   data,
			onDrop: func() {
				r.hub.metrics.IncDroppedClients()
				r.Unregister(client)
			},
		}
		if !r.hub.fanout.submit(job) {
			r.hub.metrics.IncDroppedClients()
			r.unregister(client)
		}
	}
	r.hub.metrics.ObserveBroadcastLatency(time.Since(start))
}

func (r *Room) applyOperation(ctx context.Context, sender *Client, operation crdt.Operation, clientSentAt int64) {
	if err := r.document.Apply(operation); err != nil {
		data, marshalErr := marshalEnvelope(MessageError, ErrorPayload{Message: err.Error()})
		if marshalErr == nil && sender != nil {
			sender.enqueue(data)
		}
		return
	}

	r.sequence++
	r.opsSinceSnapshot++
	r.hub.metrics.IncOperations()
	payload := OperationPayload{Operation: operation, Sequence: r.sequence, ClientSentAt: clientSentAt}
	data, err := marshalEnvelope(MessageOp, payload)
	if err != nil {
		return
	}
	r.broadcast(data, sender)
	r.queuePersistence(sender, operation)
	if r.opsSinceSnapshot >= r.hub.config.SnapshotOpThreshold {
		r.requestSnapshot()
	}
}

func (r *Room) queuePersistence(client *Client, operation crdt.Operation) {
	if r.hub.writer != nil {
		userID := int64(1)
		if client != nil && client.userID > 0 {
			userID = client.userID
		}
		_ = r.hub.writer.EnqueueOps(r.documentID, userID, []crdt.Operation{operation})
		return
	}
	// Compatible in-memory test stores do not provide a writer. Keep their
	// persistence path asynchronous without affecting the production path.
	waitFor := r.persistTail
	next := make(chan struct{})
	r.persistTail = next
	go func() {
		defer close(next)
		<-waitFor
		persistContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if attributedStore, ok := r.hub.store.(AttributedOperationStore); ok && client != nil {
			_ = attributedStore.AppendOperationsForUser(persistContext, r.documentID, client.userID, []crdt.Operation{operation})
			return
		}
		_ = r.hub.store.AppendOperations(persistContext, r.documentID, []crdt.Operation{operation})
	}()
}

func (r *Room) requestSnapshot() {
	if r.hub.writer == nil || r.opsSinceSnapshot == 0 {
		return
	}
	if r.hub.writer.EnqueueSnapshot(r.documentID, r.document.Snapshot(), r.sequence) {
		r.opsSinceSnapshot = 0
	}
}

func (r *Room) closeClients() {
	for client := range r.clients {
		r.hub.metrics.DecActiveConnections()
		r.clearCursor(client)
		client.close()
	}
	r.clients = make(map[*Client]struct{})
}

func (r *Room) applyCursor(client *Client) {
	r.cursorMu.Lock()
	payload, ok := r.pendingCursors[client]
	delete(r.pendingCursors, client)
	r.cursorQueued[client] = false
	r.cursorMu.Unlock()
	if !ok {
		return
	}
	if _, connected := r.clients[client]; !connected {
		return
	}
	payload.SiteID = client.siteID
	payload.UserID = client.userID
	payload.Username = client.username
	payload.Color = client.color
	data, err := marshalEnvelope(MessageCursor, payload)
	if err != nil {
		return
	}
	r.broadcast(data, client)
}

func (r *Room) clearCursor(client *Client) {
	r.cursorMu.Lock()
	delete(r.pendingCursors, client)
	delete(r.cursorQueued, client)
	r.cursorMu.Unlock()
}

func (r *Room) shutdown() {
	r.shutdownOnce.Do(func() {
		r.submit(roomCommand{kind: commandShutdown})
	})
}

// Register adds a client without mutating room state from the caller goroutine.
func (r *Room) Register(client *Client) {
	r.submit(roomCommand{kind: commandRegister, client: client})
}

// Unregister removes a client without mutating room state from the caller goroutine.
func (r *Room) Unregister(client *Client) {
	r.submit(roomCommand{kind: commandUnregister, client: client})
}

// SubmitOperation applies a client operation on the room owner goroutine.
func (r *Room) SubmitOperation(client *Client, operation crdt.Operation) {
	r.submit(roomCommand{kind: commandOperation, client: client, operation: operation})
}

// SubmitOperationWithTimestamp is used by load clients to measure end-to-end
// delivery without changing the CRDT operation itself.
func (r *Room) SubmitOperationWithTimestamp(client *Client, operation crdt.Operation, clientSentAt int64) {
	r.submit(roomCommand{kind: commandOperation, client: client, operation: operation, clientSentAt: clientSentAt})
}

// SubmitCursor coalesces pending updates per client and never blocks the
// client read loop on the room command queue.
func (r *Room) SubmitCursor(client *Client, payload CursorPayload) {
	if client == nil {
		return
	}
	r.cursorMu.Lock()
	r.pendingCursors[client] = payload
	shouldQueue := !r.cursorQueued[client]
	if shouldQueue {
		r.cursorQueued[client] = true
	}
	r.cursorMu.Unlock()
	if !shouldQueue {
		return
	}
	select {
	case r.commands <- roomCommand{kind: commandCursor, client: client}:
	case <-r.done:
		r.clearCursor(client)
	default:
		r.clearCursor(client)
	}
}

func (r *Room) submit(command roomCommand) {
	select {
	case r.commands <- command:
	case <-r.done:
	}
}

func (r *Room) clientQueueSize() int {
	return r.hub.config.ClientQueueSize
}

// Done exposes room termination for lifecycle tests.
func (r *Room) Done() <-chan struct{} {
	return r.done
}

type fanoutJob struct {
	client *Client
	data   []byte
	onDrop func()
}

type fanoutPool struct {
	jobs      chan fanoutJob
	closeOnce sync.Once
	workers   sync.WaitGroup
}

func newFanoutPool(workerCount, queueSize int) *fanoutPool {
	pool := &fanoutPool{jobs: make(chan fanoutJob, queueSize)}
	for range workerCount {
		pool.workers.Add(1)
		go func() {
			defer pool.workers.Done()
			for job := range pool.jobs {
				if !job.client.enqueue(job.data) && job.onDrop != nil {
					job.onDrop()
				}
			}
		}()
	}
	return pool
}

func (p *fanoutPool) submit(job fanoutJob) bool {
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}

func (p *fanoutPool) close() {
	p.closeOnce.Do(func() {
		close(p.jobs)
		p.workers.Wait()
	})
}
