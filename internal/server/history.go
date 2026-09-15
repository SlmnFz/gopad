package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/gopad/internal/cache"
	"github.com/local/gopad/internal/crdt"
	"github.com/local/gopad/internal/history"
	"github.com/local/gopad/internal/store"
)

const (
	historyCacheTTL     = 20 * time.Second
	historyCacheEntries = 512
	historyRateWindow   = time.Second
	historyRateBurst    = 30
)

// HistoryService is the durable read surface needed by the time-travel API.
// It is intentionally separate from the live room service: replay requests
// never enter a room's event loop or inspect its in-memory document.
type HistoryService interface {
	SnapshotBeforeSeq(slug string, seq int64) ([]crdt.Char, int64, error)
	OperationsInRange(slug string, fromSeq, toSeq int64) ([]crdt.Operation, error)
	HistoryBounds(slug string) (store.HistoryBounds, error)
	OperationTimestamp(slug string, seq int64) (time.Time, error)
}

type historyPointKey struct {
	slug string
	seq  int64
}

type historyPoint struct {
	Sequence  int64     `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Text      string    `json:"text"`
}

type historyRateEntry struct {
	started time.Time
	count   int
}

type historyRateLimiter struct {
	mu      sync.Mutex
	entries map[string]historyRateEntry
}

func newHistoryRateLimiter() *historyRateLimiter {
	return &historyRateLimiter{entries: make(map[string]historyRateEntry)}
}

func (limiter *historyRateLimiter) allow(key string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry := limiter.entries[key]
	if entry.started.IsZero() || now.Sub(entry.started) >= historyRateWindow {
		limiter.entries[key] = historyRateEntry{started: now, count: 1}
		return true
	}
	if entry.count >= historyRateBurst {
		return false
	}
	entry.count++
	limiter.entries[key] = entry
	return true
}

type historyHandler struct {
	service HistoryService
	points  *cache.Cache[historyPointKey, historyPoint]
	limiter *historyRateLimiter
}

func registerHistoryRoutes(mux *http.ServeMux, service HistoryService) {
	if service == nil {
		return
	}
	handler := &historyHandler{
		service: service,
		points:  cache.New[historyPointKey, historyPoint](historyCacheTTL, historyCacheEntries),
		limiter: newHistoryRateLimiter(),
	}
	mux.HandleFunc("GET /api/documents/{slug}/history/range", handler.handleRange)
	mux.HandleFunc("GET /api/documents/{slug}/history", handler.handlePoint)
}

func (handler *historyHandler) handleRange(w http.ResponseWriter, r *http.Request) {
	if !handler.allow(w, r) {
		return
	}
	slug := r.PathValue("slug")
	if !documentSlugPattern.MatchString(slug) {
		http.NotFound(w, r)
		return
	}
	bounds, err := handler.service.HistoryBounds(slug)
	if err != nil {
		handleHistoryError(w, r, err)
		return
	}
	writeHistoryJSON(w, struct {
		MinSeq     int64     `json:"minSeq"`
		MaxSeq     int64     `json:"maxSeq"`
		CreatedAt  time.Time `json:"createdAt"`
		CurrentSeq int64     `json:"currentSeq"`
	}{
		MinSeq: bounds.MinSeq, MaxSeq: bounds.MaxSeq, CreatedAt: bounds.CreatedAt, CurrentSeq: bounds.CurrentSeq,
	})
}

func (handler *historyHandler) handlePoint(w http.ResponseWriter, r *http.Request) {
	if !handler.allow(w, r) {
		return
	}
	slug := r.PathValue("slug")
	if !documentSlugPattern.MatchString(slug) {
		http.NotFound(w, r)
		return
	}
	requestedSeq := int64(0)
	if rawSeq := strings.TrimSpace(r.URL.Query().Get("seq")); rawSeq != "" {
		parsed, err := strconv.ParseInt(rawSeq, 10, 64)
		if err != nil {
			http.Error(w, "seq must be an integer", http.StatusBadRequest)
			return
		}
		requestedSeq = parsed
	}
	bounds, err := handler.service.HistoryBounds(slug)
	if err != nil {
		handleHistoryError(w, r, err)
		return
	}
	sequence := clampHistorySequence(requestedSeq, bounds.MinSeq, bounds.MaxSeq)
	key := historyPointKey{slug: slug, seq: sequence}
	if point, ok := handler.points.Get(key); ok {
		writeHistoryJSON(w, point)
		return
	}

	snapshot, snapshotSeq, err := handler.service.SnapshotBeforeSeq(slug, sequence)
	if err != nil {
		handleHistoryError(w, r, err)
		return
	}
	operations, err := handler.service.OperationsInRange(slug, snapshotSeq, sequence)
	if err != nil {
		handleHistoryError(w, r, err)
		return
	}
	document, err := history.BuildAt(snapshot, operations)
	if err != nil {
		http.Error(w, "could not reconstruct history", http.StatusInternalServerError)
		return
	}
	timestamp := bounds.CreatedAt
	if sequence > 0 {
		if operationTimestamp, timestampErr := handler.service.OperationTimestamp(slug, sequence); timestampErr == nil && !operationTimestamp.IsZero() {
			timestamp = operationTimestamp
		}
	}
	point := historyPoint{Sequence: sequence, Timestamp: timestamp, Text: document.Text()}
	handler.points.Set(key, point)
	writeHistoryJSON(w, point)
}

func (handler *historyHandler) allow(w http.ResponseWriter, r *http.Request) bool {
	if handler.limiter.allow(historyClientIP(r), time.Now()) {
		return true
	}
	w.Header().Set("Retry-After", "1")
	http.Error(w, "history request rate limit exceeded", http.StatusTooManyRequests)
	return false
}

func historyClientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	if realIP := strings.TrimSpace(r.Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func clampHistorySequence(sequence, minSeq, maxSeq int64) int64 {
	if sequence < minSeq {
		return minSeq
	}
	if sequence > maxSeq {
		return maxSeq
	}
	return sequence
}

func handleHistoryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrDocumentNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "could not load document history", http.StatusInternalServerError)
}

func writeHistoryJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}
