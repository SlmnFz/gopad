package realtime

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/local/gopad/internal/crdt"
	gopadmetrics "github.com/local/gopad/internal/metrics"
	"github.com/local/gopad/internal/store"
)

const (
	defaultRoomIdleTimeout = 45 * time.Second
	defaultClientQueueSize = 64
	defaultFanoutQueueSize = 256
	defaultFanoutWorkers   = 1
)

// Store is the persistence surface needed by realtime rooms.
type Store interface {
	LoadDocument(context.Context, string) (store.LoadedDocument, error)
	AppendOperations(context.Context, int64, []crdt.Operation) error
	FindOrCreateUser(context.Context, string) (store.User, error)
}

// AttributedOperationStore is optional for compatible test stores; the real
// SQLite store uses it to attach each operation to the active username.
type AttributedOperationStore interface {
	AppendOperationsForUser(context.Context, int64, int64, []crdt.Operation) error
}

// PersistenceWriter is the non-blocking write-behind surface used by rooms.
type PersistenceWriter interface {
	EnqueueOps(documentID, userID int64, operations []crdt.Operation) bool
	EnqueueSnapshot(documentID int64, chars []crdt.Char, version int64) bool
	Flush(context.Context) error
	Close(context.Context) error
}

// HubConfig controls room lifecycle and bounded realtime queues.
type HubConfig struct {
	IdleTimeout         time.Duration
	ClientQueueSize     int
	FanoutQueueSize     int
	FanoutWorkers       int
	SnapshotOpThreshold int
	SnapshotInterval    time.Duration
	Writer              PersistenceWriter
	Metrics             *gopadmetrics.Metrics
}

// Hub owns the active room registry and shared fan-out workers.
type Hub struct {
	store   Store
	rooms   sync.Map
	config  HubConfig
	fanout  *fanoutPool
	ctx     context.Context
	writer  PersistenceWriter
	metrics *gopadmetrics.Metrics

	closeOnce sync.Once
	closeErr  error
}

// NewHub creates a hub with production defaults. The store must be non-nil.
func NewHub(database Store) *Hub {
	metrics := gopadmetrics.New()
	snapshotOpThreshold, snapshotInterval := store.SnapshotConfigFromEnv()
	var writer PersistenceWriter
	if sqliteStore, ok := database.(*store.Store); ok {
		writer = store.NewWriterWithConfig(sqliteStore, store.WriterConfigFromEnv(metrics))
	}
	return NewHubWithConfig(database, HubConfig{
		IdleTimeout:         roomIdleTimeoutFromEnv(),
		ClientQueueSize:     defaultClientQueueSize,
		FanoutQueueSize:     defaultFanoutQueueSize,
		FanoutWorkers:       defaultFanoutWorkers,
		SnapshotOpThreshold: snapshotOpThreshold,
		SnapshotInterval:    snapshotInterval,
		Writer:              writer,
		Metrics:             metrics,
	})
}

// NewHubWithConfig is useful for deterministic room lifecycle tests.
func NewHubWithConfig(database Store, config HubConfig) *Hub {
	if config.IdleTimeout <= 0 {
		config.IdleTimeout = defaultRoomIdleTimeout
	}
	if config.ClientQueueSize <= 0 {
		config.ClientQueueSize = defaultClientQueueSize
	}
	if config.FanoutQueueSize <= 0 {
		config.FanoutQueueSize = defaultFanoutQueueSize
	}
	if config.FanoutWorkers <= 0 {
		config.FanoutWorkers = defaultFanoutWorkers
	}
	if config.SnapshotOpThreshold <= 0 {
		config.SnapshotOpThreshold = store.DefaultSnapshotOpThreshold
	}
	if config.SnapshotInterval <= 0 {
		config.SnapshotInterval = store.DefaultSnapshotInterval
	}
	if config.Metrics == nil {
		config.Metrics = gopadmetrics.New()
	}
	return &Hub{
		store:   database,
		config:  config,
		fanout:  newFanoutPool(config.FanoutWorkers, config.FanoutQueueSize),
		ctx:     context.Background(),
		writer:  config.Writer,
		metrics: config.Metrics,
	}
}

// GetOrCreateRoom loads a document and returns its one active room instance.
func (h *Hub) GetOrCreateRoom(ctx context.Context, slug string) (*Room, error) {
	if h == nil || h.store == nil {
		return nil, errors.New("realtime hub is not configured")
	}
	if existing, ok := h.rooms.Load(slug); ok {
		return existing.(*Room), nil
	}

	loaded, err := h.store.LoadDocument(ctx, slug)
	if err != nil {
		return nil, err
	}
	document, err := documentFromLoaded(loaded)
	if err != nil {
		return nil, err
	}
	room := newRoom(h, slug, loaded.ID, document, loaded.SnapshotVersion, loaded.Operations)
	actual, loadedByOther := h.rooms.LoadOrStore(slug, room)
	if loadedByOther {
		return actual.(*Room), nil
	}
	h.metrics.AddActiveRooms(1)
	go room.run(h.ctx)
	return room, nil
}

// ServeHTTP upgrades a valid document route to a WebSocket room.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !validSlug(slug) {
		http.NotFound(w, r)
		return
	}
	room, err := h.GetOrCreateRoom(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrDocumentNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "could not load room", http.StatusInternalServerError)
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	client := newWebSocketClient(connection, room.clientQueueSize())
	client.serve(r.Context(), room)
}

// Close stops accepting room work, flushes accepted persistence, closes active
// clients, and stops fan-out workers. It is safe to call more than once.
func (h *Hub) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.CloseContext(ctx)
}

// CloseContext performs the graceful shutdown sequence with a caller-owned
// timeout. The writer is closed only after every room has requested its final
// snapshot and all room goroutines have stopped.
func (h *Hub) CloseContext(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		rooms := make([]*Room, 0)
		h.rooms.Range(func(_, value any) bool {
			rooms = append(rooms, value.(*Room))
			return true
		})
		for _, room := range rooms {
			room.shutdown()
		}
		for _, room := range rooms {
			<-room.Done()
		}
		if h.writer != nil {
			h.closeErr = h.writer.Close(ctx)
		}
		h.fanout.close()
	})
	return h.closeErr
}

func (h *Hub) removeRoom(room *Room) {
	if h.rooms.CompareAndDelete(room.slug, room) {
		h.metrics.AddActiveRooms(-1)
	}
}

// Metrics returns the registry shared by realtime and HTTP instrumentation.
func (h *Hub) Metrics() *gopadmetrics.Metrics {
	if h == nil {
		return nil
	}
	return h.metrics
}

func roomIdleTimeoutFromEnv() time.Duration {
	value := strings.TrimSpace(os.Getenv("GOPAD_ROOM_IDLE_TIMEOUT"))
	if value == "" {
		return defaultRoomIdleTimeout
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return defaultRoomIdleTimeout
}

func validSlug(slug string) bool {
	if len(slug) != 12 {
		return false
	}
	for _, char := range slug {
		if !((char >= '0' && char <= '9') || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')) {
			return false
		}
	}
	return true
}
