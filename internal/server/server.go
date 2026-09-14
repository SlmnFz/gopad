package server

import (
	"context"
	"io"
	"net/http"

	"github.com/local/gopad/internal/store"
)

// DocumentService is the persistence surface needed by the document routes.
type DocumentService interface {
	CreateDocument(context.Context) (store.Document, error)
	LoadDocument(context.Context, string) (store.LoadedDocument, error)
}

// New returns the HTTP handler for the Gopad server.
func New(services ...DocumentService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})

	var service DocumentService
	if len(services) > 0 {
		service = services[0]
	}
	registerDocumentRoutes(mux, service)
	return mux
}
