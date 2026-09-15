package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/local/gopad/internal/crdt"
)

type fakeWriterExecutor struct {
	mu               sync.Mutex
	operations       [][]OperationWrite
	snapshots        []snapshotJob
	operationStarted chan struct{}
	blockOperations  <-chan struct{}
}

func (executor *fakeWriterExecutor) AppendOperations(_ context.Context, _ int64, _ []crdt.Operation) error {
	return nil
}

func (executor *fakeWriterExecutor) AppendOperationBatch(_ context.Context, _ int64, writes []OperationWrite) error {
	if executor.operationStarted != nil {
		select {
		case <-executor.operationStarted:
		default:
			close(executor.operationStarted)
		}
	}
	if executor.blockOperations != nil {
		<-executor.blockOperations
	}
	executor.mu.Lock()
	executor.operations = append(executor.operations, append([]OperationWrite(nil), writes...))
	executor.mu.Unlock()
	return nil
}

func (executor *fakeWriterExecutor) SaveSnapshot(_ context.Context, documentID int64, chars []crdt.Char, version int64) error {
	executor.mu.Lock()
	executor.snapshots = append(executor.snapshots, snapshotJob{documentID: documentID, chars: cloneChars(chars), version: version})
	executor.mu.Unlock()
	return nil
}

type fakeWriterObserver struct {
	mu      sync.Mutex
	depth   int
	dropped int
	batches []int
}

func (observer *fakeWriterObserver) SetWriteQueueDepth(depth int) {
	observer.mu.Lock()
	observer.depth = depth
	observer.mu.Unlock()
}

func (observer *fakeWriterObserver) IncWriteQueueDropped() {
	observer.mu.Lock()
	observer.dropped++
	observer.mu.Unlock()
}

func (observer *fakeWriterObserver) ObserveWriteBatch(size int, _ time.Duration) {
	observer.mu.Lock()
	observer.batches = append(observer.batches, size)
	observer.mu.Unlock()
}

type fakeWriterClock struct {
	ready chan struct{}
	timer *fakeWriterTimer
}

func newFakeWriterClock() *fakeWriterClock {
	return &fakeWriterClock{ready: make(chan struct{})}
}

func (clock *fakeWriterClock) NewTimer(_ time.Duration) Timer {
	clock.timer = &fakeWriterTimer{channel: make(chan time.Time, 1)}
	close(clock.ready)
	return clock.timer
}

func (clock *fakeWriterClock) fire() {
	clock.timer.channel <- time.Now()
}

type fakeWriterTimer struct {
	mu      sync.Mutex
	channel chan time.Time
	stopped bool
}

func (timer *fakeWriterTimer) Chan() <-chan time.Time { return timer.channel }

func (timer *fakeWriterTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasRunning := !timer.stopped
	timer.stopped = true
	return wasRunning
}

func (timer *fakeWriterTimer) Reset(time.Duration) bool {
	timer.mu.Lock()
	timer.stopped = false
	timer.mu.Unlock()
	return true
}

func TestWriterFlushesAtOperationThreshold(t *testing.T) {
	executor := &fakeWriterExecutor{}
	clock := newFakeWriterClock()
	writer := NewWriterWithConfig(executor, WriterConfig{
		QueueSize:           128,
		OperationBatchSize:  100,
		OperationFlushDelay: time.Hour,
		Clock:               clock,
	})
	defer closeWriter(t, writer)
	<-clock.ready

	for counter := uint64(1); counter <= 100; counter++ {
		if !writer.EnqueueOps(7, 9, []crdt.Operation{{Type: crdt.Insert, ID: crdt.CharID{SiteID: "test", Counter: counter}}}) {
			t.Fatal("operation was rejected before the queue filled")
		}
	}

	waitFor(t, func() bool {
		executor.mu.Lock()
		defer executor.mu.Unlock()
		return len(executor.operations) == 1
	})
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.operations[0]) != 100 {
		t.Fatalf("operation batch size = %d, want 100", len(executor.operations[0]))
	}
}

func TestWriterFlushesAtTimer(t *testing.T) {
	executor := &fakeWriterExecutor{}
	clock := newFakeWriterClock()
	writer := NewWriterWithConfig(executor, WriterConfig{
		QueueSize:           8,
		OperationBatchSize:  100,
		OperationFlushDelay: time.Hour,
		Clock:               clock,
	})
	defer closeWriter(t, writer)
	<-clock.ready
	if !writer.EnqueueOps(7, 9, []crdt.Operation{{Type: crdt.Insert, ID: crdt.CharID{SiteID: "test", Counter: 1}}}) {
		t.Fatal("operation was rejected")
	}
	clock.fire()
	waitFor(t, func() bool {
		executor.mu.Lock()
		defer executor.mu.Unlock()
		return len(executor.operations) == 1
	})
}

func TestWriterCoalescesPendingSnapshots(t *testing.T) {
	executor := &fakeWriterExecutor{}
	clock := newFakeWriterClock()
	writer := NewWriterWithConfig(executor, WriterConfig{QueueSize: 8, Clock: clock})
	defer closeWriter(t, writer)
	<-clock.ready
	chars := []crdt.Char{{ID: crdt.CharID{SiteID: "test", Counter: 1}, Value: 'A'}}
	if !writer.EnqueueSnapshot(7, chars, 1) || !writer.EnqueueSnapshot(7, chars, 2) {
		t.Fatal("snapshot was rejected")
	}
	if err := writer.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if len(executor.snapshots) != 1 || executor.snapshots[0].version != 2 {
		t.Fatalf("snapshots = %#v, want one latest snapshot", executor.snapshots)
	}
}

func TestWriterReportsBackpressureWithoutBlocking(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	executor := &fakeWriterExecutor{operationStarted: started, blockOperations: block}
	observer := &fakeWriterObserver{}
	writer := NewWriterWithConfig(executor, WriterConfig{
		QueueSize:           1,
		OperationBatchSize:  1,
		OperationFlushDelay: time.Hour,
		Observer:            observer,
	})
	defer closeWriter(t, writer)
	operation := []crdt.Operation{{Type: crdt.Insert, ID: crdt.CharID{SiteID: "test", Counter: 1}}}
	if !writer.EnqueueOps(7, 9, operation) {
		t.Fatal("first operation was rejected")
	}
	<-started
	if !writer.EnqueueOps(7, 9, operation) {
		t.Fatal("second operation should fit the bounded queue")
	}
	if writer.EnqueueOps(7, 9, operation) {
		t.Fatal("third operation should report a full queue")
	}
	observer.mu.Lock()
	dropped := observer.dropped
	observer.mu.Unlock()
	if dropped != 1 {
		t.Fatalf("queue drops = %d, want 1", dropped)
	}
	close(block)
}

func TestWriterCloseWaitsForFinalFlush(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{})
	executor := &fakeWriterExecutor{operationStarted: started, blockOperations: block}
	writer := NewWriterWithConfig(executor, WriterConfig{QueueSize: 4, OperationBatchSize: 1})
	if !writer.EnqueueOps(7, 9, []crdt.Operation{{Type: crdt.Insert, ID: crdt.CharID{SiteID: "test", Counter: 1}}}) {
		t.Fatal("operation was rejected")
	}
	<-started

	closeResult := make(chan error, 1)
	go func() {
		context, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closeResult <- writer.Close(context)
	}()
	select {
	case err := <-closeResult:
		t.Fatalf("writer closed before final flush: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(block)
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("close writer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not finish after final flush was released")
	}
}

func closeWriter(t *testing.T, writer *Writer) {
	t.Helper()
	context, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := writer.Close(context); err != nil {
		t.Fatalf("close writer: %v", err)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
