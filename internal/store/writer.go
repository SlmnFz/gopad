package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/gopad/internal/crdt"
)

const (
	DefaultWriterQueueSize     = 1024
	DefaultOperationBatchSize  = 100
	DefaultOperationFlushDelay = 250 * time.Millisecond
	DefaultSnapshotOpThreshold = 1000
	DefaultSnapshotInterval    = 30 * time.Second
)

// OperationWrite keeps the username attribution attached to an operation
// while preserving the room's canonical operation order.
type OperationWrite struct {
	UserID    int64
	Operation crdt.Operation
}

// WriterExecutor is the durable write surface used by Writer. Store provides
// this interface; tests can provide a small in-memory fake.
type WriterExecutor interface {
	AppendOperations(context.Context, int64, []crdt.Operation) error
	SaveSnapshot(context.Context, int64, []crdt.Char, int64) error
}

// BatchOperationExecutor is optional. The SQLite store implements it so
// operations from different users can be committed in one transaction without
// losing their order or attribution.
type BatchOperationExecutor interface {
	AppendOperationBatch(context.Context, int64, []OperationWrite) error
}

// WriterObserver receives non-blocking queue and batch signals. The metrics
// package implements this interface without coupling persistence to metrics.
type WriterObserver interface {
	SetWriteQueueDepth(int)
	IncWriteQueueDropped()
	ObserveWriteBatch(int, time.Duration)
}

// Timer and Clock make batching tests deterministic without changing the
// production time source.
type Timer interface {
	Chan() <-chan time.Time
	Stop() bool
	Reset(time.Duration) bool
}

type Clock interface {
	NewTimer(time.Duration) Timer
}

type realClock struct{}

func (realClock) NewTimer(duration time.Duration) Timer {
	return &realTimer{timer: time.NewTimer(duration)}
}

type realTimer struct {
	timer *time.Timer
}

func (timer *realTimer) Chan() <-chan time.Time { return timer.timer.C }
func (timer *realTimer) Stop() bool             { return timer.timer.Stop() }
func (timer *realTimer) Reset(duration time.Duration) bool {
	return timer.timer.Reset(duration)
}

// WriterConfig controls the bounded background write queue.
type WriterConfig struct {
	QueueSize           int
	OperationBatchSize  int
	OperationFlushDelay time.Duration
	Observer            WriterObserver
	Clock               Clock
}

func defaultWriterConfig() WriterConfig {
	return WriterConfig{
		QueueSize:           DefaultWriterQueueSize,
		OperationBatchSize:  DefaultOperationBatchSize,
		OperationFlushDelay: DefaultOperationFlushDelay,
		Clock:               realClock{},
	}
}

// WriterConfigFromEnv returns production settings while keeping all defaults
// explicit for local runs and tests.
func WriterConfigFromEnv(observer WriterObserver) WriterConfig {
	config := defaultWriterConfig()
	config.Observer = observer
	config.QueueSize = positiveEnvInt("GOPAD_WRITE_QUEUE_SIZE", config.QueueSize)
	config.OperationBatchSize = positiveEnvInt("GOPAD_OP_BATCH_SIZE", config.OperationBatchSize)
	config.OperationFlushDelay = positiveEnvDuration("GOPAD_OP_FLUSH_INTERVAL", config.OperationFlushDelay)
	return config
}

// SnapshotConfigFromEnv returns the room snapshot thresholds. Snapshot state
// is copied by the room owner and persisted by Writer, so these values live
// alongside writer configuration while remaining independently tunable.
func SnapshotConfigFromEnv() (int, time.Duration) {
	return positiveEnvInt("GOPAD_SNAPSHOT_OP_THRESHOLD", DefaultSnapshotOpThreshold),
		positiveEnvDuration("GOPAD_SNAPSHOT_INTERVAL", DefaultSnapshotInterval)
}

func positiveEnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func positiveEnvDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}
	return duration
}

type writerJobKind uint8

const (
	writerOperations writerJobKind = iota
	writerSnapshotSignal
	writerFlush
	writerClose
)

type writerJob struct {
	kind       writerJobKind
	documentID int64
	userID     int64
	operations []crdt.Operation
	response   chan error
}

type snapshotJob struct {
	documentID int64
	chars      []crdt.Char
	version    int64
}

type queuedOperation struct {
	userID    int64
	operation crdt.Operation
}

// Writer serializes all operation and snapshot writes through one goroutine.
// Enqueue methods never wait for SQLite or for the room owner.
type Writer struct {
	executor WriterExecutor
	config   WriterConfig
	jobs     chan writerJob
	done     chan struct{}

	snapshotMu      sync.Mutex
	pendingSnapshot map[int64]snapshotJob
	snapshotQueued  bool
	closed          bool

	closeOnce sync.Once
	closeMu   sync.Mutex
	closeErr  error
}

// NewWriter creates a production writer using the default thresholds.
func NewWriter(executor WriterExecutor) *Writer {
	return NewWriterWithConfig(executor, WriterConfig{})
}

// NewWriterWithConfig is useful for tests and for bounded deployments.
func NewWriterWithConfig(executor WriterExecutor, config WriterConfig) *Writer {
	defaults := defaultWriterConfig()
	if config.QueueSize <= 0 {
		config.QueueSize = defaults.QueueSize
	}
	if config.OperationBatchSize <= 0 {
		config.OperationBatchSize = defaults.OperationBatchSize
	}
	if config.OperationFlushDelay <= 0 {
		config.OperationFlushDelay = defaults.OperationFlushDelay
	}
	if config.Clock == nil {
		config.Clock = defaults.Clock
	}
	writer := &Writer{
		executor:        executor,
		config:          config,
		jobs:            make(chan writerJob, config.QueueSize),
		done:            make(chan struct{}),
		pendingSnapshot: make(map[int64]snapshotJob),
	}
	go writer.run()
	return writer
}

// EnqueueOps adds operations to the bounded queue. It returns false when the
// queue is full or the writer has already stopped; callers must not block a
// room owner waiting for persistence.
func (writer *Writer) EnqueueOps(documentID, userID int64, operations []crdt.Operation) bool {
	if writer == nil || len(operations) == 0 {
		return writer != nil
	}
	job := writerJob{
		kind:       writerOperations,
		documentID: documentID,
		userID:     userID,
		operations: append([]crdt.Operation(nil), operations...),
	}
	writer.snapshotMu.Lock()
	closed := writer.closed
	writer.snapshotMu.Unlock()
	if closed {
		return false
	}
	select {
	case writer.jobs <- job:
		writer.observeQueueDepth()
		return true
	default:
		writer.dropQueueItem()
		return false
	}
}

// EnqueueSnapshot replaces any pending snapshot for the same document and
// queues at most one snapshot signal. The CRDT slice is copied before the
// caller returns, so the room may continue mutating its document.
func (writer *Writer) EnqueueSnapshot(documentID int64, chars []crdt.Char, version int64) bool {
	if writer == nil {
		return false
	}
	job := snapshotJob{documentID: documentID, chars: cloneChars(chars), version: version}
	writer.snapshotMu.Lock()
	if writer.closed {
		writer.snapshotMu.Unlock()
		return false
	}
	writer.pendingSnapshot[documentID] = job
	shouldQueue := !writer.snapshotQueued
	if shouldQueue {
		writer.snapshotQueued = true
	}
	writer.snapshotMu.Unlock()
	if !shouldQueue {
		return true
	}
	select {
	case writer.jobs <- writerJob{kind: writerSnapshotSignal}:
		writer.observeQueueDepth()
		return true
	default:
		writer.snapshotMu.Lock()
		writer.snapshotQueued = false
		writer.snapshotMu.Unlock()
		writer.dropQueueItem()
		return false
	}
}

// Flush waits until all accepted operations and snapshots ahead of the
// barrier have been written.
func (writer *Writer) Flush(ctx context.Context) error {
	if writer == nil {
		return nil
	}
	response := make(chan error, 1)
	if err := writer.enqueueBarrier(ctx, writerJob{kind: writerFlush, response: response}); err != nil {
		return err
	}
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close flushes accepted work, stops the writer goroutine, and is safe to
// call more than once.
func (writer *Writer) Close(ctx context.Context) error {
	if writer == nil {
		return nil
	}
	writer.closeOnce.Do(func() {
		response := make(chan error, 1)
		if err := writer.enqueueBarrier(ctx, writerJob{kind: writerClose, response: response}); err != nil {
			writer.closeMu.Lock()
			writer.closeErr = err
			writer.closeMu.Unlock()
			return
		}
		select {
		case err := <-response:
			writer.closeMu.Lock()
			writer.closeErr = err
			writer.closeMu.Unlock()
		case <-ctx.Done():
			writer.closeMu.Lock()
			writer.closeErr = ctx.Err()
			writer.closeMu.Unlock()
		}
	})
	writer.closeMu.Lock()
	defer writer.closeMu.Unlock()
	return writer.closeErr
}

func (writer *Writer) enqueueBarrier(ctx context.Context, job writerJob) error {
	writer.snapshotMu.Lock()
	closed := writer.closed
	writer.snapshotMu.Unlock()
	if closed {
		return errors.New("writer is closed")
	}
	select {
	case writer.jobs <- job:
		writer.observeQueueDepth()
		return nil
	case <-writer.done:
		return errors.New("writer is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (writer *Writer) run() {
	timer := writer.config.Clock.NewTimer(writer.config.OperationFlushDelay)
	if !timer.Stop() {
		select {
		case <-timer.Chan():
		default:
		}
	}
	timerActive := false
	pending := make(map[int64][]queuedOperation)
	pendingCount := 0
	defer close(writer.done)

	flush := func() error {
		if pendingCount == 0 {
			return nil
		}
		start := time.Now()
		batches := pending
		pending = make(map[int64][]queuedOperation)
		batchSize := pendingCount
		pendingCount = 0
		var firstErr error
		for documentID, operations := range batches {
			writes := make([]OperationWrite, 0, len(operations))
			for _, operation := range operations {
				writes = append(writes, OperationWrite{UserID: operation.userID, Operation: operation.operation})
			}
			var err error
			if batchExecutor, ok := writer.executor.(BatchOperationExecutor); ok {
				err = batchExecutor.AppendOperationBatch(context.Background(), documentID, writes)
			} else {
				err = appendFallbackBatch(writer.executor, documentID, writes)
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if firstErr == nil && writer.config.Observer != nil {
			writer.config.Observer.ObserveWriteBatch(batchSize, time.Since(start))
		}
		return firstErr
	}

	flushSnapshots := func() error {
		snapshots := writer.takeSnapshots()
		var firstErr error
		for _, snapshot := range snapshots {
			if err := writer.executor.SaveSnapshot(context.Background(), snapshot.documentID, snapshot.chars, snapshot.version); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	resetTimer := func() {
		if timerActive && !timer.Stop() {
			select {
			case <-timer.Chan():
			default:
			}
		}
		timer.Reset(writer.config.OperationFlushDelay)
		timerActive = true
	}
	stopTimer := func() {
		if timerActive {
			timer.Stop()
			timerActive = false
		}
	}

	for {
		select {
		case job := <-writer.jobs:
			writer.observeQueueDepth()
			switch job.kind {
			case writerOperations:
				for _, operation := range job.operations {
					pending[job.documentID] = append(pending[job.documentID], queuedOperation{userID: job.userID, operation: operation})
					pendingCount++
				}
				resetTimer()
				if pendingCount >= writer.config.OperationBatchSize {
					stopTimer()
					_ = flush()
				}
			case writerSnapshotSignal:
				stopTimer()
				flushErr := flush()
				snapshotErr := flushSnapshots()
				if job.response != nil {
					job.response <- errors.Join(flushErr, snapshotErr)
				}
			case writerFlush:
				stopTimer()
				job.response <- errors.Join(flush(), flushSnapshots())
			case writerClose:
				stopTimer()
				err := errors.Join(flush(), flushSnapshots())
				writer.snapshotMu.Lock()
				writer.closed = true
				writer.snapshotMu.Unlock()
				job.response <- err
				return
			}
		case <-timer.Chan():
			timerActive = false
			_ = flush()
		}
	}
}

func (writer *Writer) takeSnapshots() []snapshotJob {
	writer.snapshotMu.Lock()
	writer.snapshotQueued = false
	snapshots := make([]snapshotJob, 0, len(writer.pendingSnapshot))
	for documentID, snapshot := range writer.pendingSnapshot {
		if snapshot.documentID == 0 {
			snapshot.documentID = documentID
		}
		snapshots = append(snapshots, snapshot)
		delete(writer.pendingSnapshot, documentID)
	}
	writer.snapshotMu.Unlock()
	return snapshots
}

func (writer *Writer) observeQueueDepth() {
	if writer.config.Observer != nil {
		writer.config.Observer.SetWriteQueueDepth(len(writer.jobs))
	}
}

func (writer *Writer) dropQueueItem() {
	if writer.config.Observer != nil {
		writer.config.Observer.IncWriteQueueDropped()
		writer.config.Observer.SetWriteQueueDepth(len(writer.jobs))
	}
}

func appendFallbackBatch(executor WriterExecutor, documentID int64, writes []OperationWrite) error {
	for start := 0; start < len(writes); {
		end := start + 1
		for end < len(writes) && writes[end].UserID == writes[start].UserID {
			end++
		}
		operations := make([]crdt.Operation, 0, end-start)
		for _, write := range writes[start:end] {
			operations = append(operations, write.Operation)
		}
		if err := executor.AppendOperations(context.Background(), documentID, operations); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func cloneChars(chars []crdt.Char) []crdt.Char {
	if chars == nil {
		return nil
	}
	copyOfChars := make([]crdt.Char, len(chars))
	for index, char := range chars {
		copyOfChars[index] = char
		if char.LeftID != nil {
			leftID := *char.LeftID
			copyOfChars[index].LeftID = &leftID
		}
	}
	return copyOfChars
}
