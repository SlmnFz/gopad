package realtime

import (
	"context"
	"fmt"
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
	commandShutdown
)

type roomCommand struct {
	kind      roomCommandKind
	client    *Client
	operation crdt.Operation
}

// Room serializes all canonical CRDT mutation through one owner goroutine.
type Room struct {
	hub         *Hub
	slug        string
	documentID  int64
	document    *crdt.Document
	sequence    int64
	operations  []store.OperationRecord
	persistTail <-chan struct{}

	commands chan roomCommand
	done     chan struct{}
	clients  map[*Client]struct{}

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
		hub:         hub,
		slug:        slug,
		documentID:  documentID,
		document:    document,
		sequence:    sequence,
		operations:  operations,
		persistTail: persisted,
		commands:    make(chan roomCommand, hub.config.ClientQueueSize),
		done:        make(chan struct{}),
		clients:     make(map[*Client]struct{}),
	}
}

func documentFromLoaded(loaded store.LoadedDocument) (*crdt.Document, error) {
	document := crdt.New()
	for _, char := range loaded.Snapshot {
		if err := document.Apply(crdt.Operation{
			Type:   crdt.Insert,
			ID:     char.ID,
			Value:  char.Value,
			LeftID: char.LeftID,
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
				r.applyOperation(ctx, command.client, command.operation)
			case commandShutdown:
				r.closeClients()
				return
			}
		case <-idleC:
			if len(r.clients) == 0 {
				r.hub.removeRoom(r)
				return
			}
			idleC = nil
			idleTimer = nil
		case <-ctx.Done():
			r.closeClients()
			return
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
		Snapshot: r.document.Snapshot(),
		Sequence: r.sequence,
		Presence: []PresenceIdentity{},
	}
	data, err := marshalEnvelope(MessageSync, payload)
	if err != nil || !client.enqueue(data) {
		delete(r.clients, client)
		client.close()
	}
}

func (r *Room) unregister(client *Client) {
	if client == nil {
		return
	}
	if _, ok := r.clients[client]; ok {
		delete(r.clients, client)
		client.close()
	}
}

func (r *Room) applyOperation(ctx context.Context, sender *Client, operation crdt.Operation) {
	if err := r.document.Apply(operation); err != nil {
		data, marshalErr := marshalEnvelope(MessageError, ErrorPayload{Message: err.Error()})
		if marshalErr == nil && sender != nil {
			sender.enqueue(data)
		}
		return
	}

	r.sequence++
	payload := OperationPayload{Operation: operation, Sequence: r.sequence}
	data, err := marshalEnvelope(MessageOp, payload)
	if err != nil {
		return
	}
	for client := range r.clients {
		if client == sender {
			continue
		}
		job := fanoutJob{
			client: client,
			data:   data,
			onDrop: func() { r.Unregister(client) },
		}
		if !r.hub.fanout.submit(job) {
			r.unregister(client)
		}
	}

	r.queuePersistence(operation)
}

// queuePersistence chains asynchronous writes in room sequence order without
// making the room owner wait for the store. Task 8 replaces this bridge with
// the shared batching writer.
func (r *Room) queuePersistence(operation crdt.Operation) {
	waitFor := r.persistTail
	next := make(chan struct{})
	r.persistTail = next
	go func() {
		defer close(next)
		<-waitFor
		persistContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.hub.store.AppendOperations(persistContext, r.documentID, []crdt.Operation{operation})
	}()
}

func (r *Room) closeClients() {
	for client := range r.clients {
		client.close()
	}
	r.clients = make(map[*Client]struct{})
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
