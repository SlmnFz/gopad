package server

import (
	"context"
	"io"
	"net/http"

	"github.com/local/gopad/internal/cache"
	"github.com/local/gopad/internal/metrics"
	"github.com/local/gopad/internal/store"
)

// DocumentService is the persistence surface needed by the document routes.
type DocumentService interface {
	CreateDocument(context.Context) (store.Document, error)
	LoadDocument(context.Context, string) (store.LoadedDocument, error)
}

// New returns the HTTP handler for the Gopad server.
func New(dependencies ...any) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})

	var service DocumentService
	var historyService HistoryService
	var websocketHandler http.Handler
	var metricsHandler http.Handler
	var slugCache *cache.SlugCache
	for _, dependency := range dependencies {
		if candidate, ok := dependency.(*cache.SlugCache); ok {
			slugCache = candidate
			continue
		}
		if provider, ok := dependency.(interface{ SlugCache() *cache.SlugCache }); ok {
			slugCache = provider.SlugCache()
		}
		if candidate, ok := dependency.(*metrics.Metrics); ok {
			metricsHandler = candidate
			continue
		}
		if candidate, ok := dependency.(DocumentService); ok {
			service = candidate
		}
		if candidate, ok := dependency.(HistoryService); ok {
			historyService = candidate
		}
		if candidate, ok := dependency.(http.Handler); ok {
			websocketHandler = candidate
		}
	}
	registerDocumentRoutes(mux, service, slugCache)
	registerHistoryRoutes(mux, historyService)
	if websocketHandler != nil {
		mux.Handle("GET /ws/{slug}", websocketHandler)
	}
	if metricsHandler != nil {
		mux.Handle("GET /metrics", metricsHandler)
	}
	return mux
}
