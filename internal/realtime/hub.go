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

// HubConfig controls room lifecycle and bounded realtime queues.
type HubConfig struct {
	IdleTimeout     time.Duration
	ClientQueueSize int
	FanoutQueueSize int
	FanoutWorkers   int
}

// Hub owns the active room registry and shared fan-out workers.
type Hub struct {
	store  Store
	rooms  sync.Map
	config HubConfig
	fanout *fanoutPool
	ctx    context.Context

	closeOnce sync.Once
}

// NewHub creates a hub with production defaults. The store must be non-nil.
func NewHub(database Store) *Hub {
	return NewHubWithConfig(database, HubConfig{
		IdleTimeout:     roomIdleTimeoutFromEnv(),
		ClientQueueSize: defaultClientQueueSize,
		FanoutQueueSize: defaultFanoutQueueSize,
		FanoutWorkers:   defaultFanoutWorkers,
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
	return &Hub{
		store:  database,
		config: config,
		fanout: newFanoutPool(config.FanoutWorkers, config.FanoutQueueSize),
		ctx:    context.Background(),
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

// Close stops accepting room work, closes active clients, and stops fan-out
// workers. It is safe to call more than once.
func (h *Hub) Close() {
	if h == nil {
		return
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
		h.fanout.close()
	})
}

func (h *Hub) removeRoom(room *Room) {
	h.rooms.CompareAndDelete(room.slug, room)
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
