package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"regexp"

	"github.com/local/gopad/internal/store"
	"github.com/local/gopad/web"
)

var documentSlugPattern = regexp.MustCompile(`^[0-9A-Za-z]{12}$`)

func registerDocumentRoutes(mux *http.ServeMux, service DocumentService) {
	assets, err := fs.Sub(web.FS, "assets")
	if err != nil {
		panic(err)
	}

	mux.HandleFunc("GET /", handleLandingPage)
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("POST /documents", func(w http.ResponseWriter, r *http.Request) {
		handleCreateDocument(w, r, service)
	})
	mux.HandleFunc("GET /d/{slug}", func(w http.ResponseWriter, r *http.Request) {
		handleEditorPage(w, r, service)
	})
}

func handleLandingPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	servePage(w, "index.html")
}

func handleCreateDocument(w http.ResponseWriter, r *http.Request, service DocumentService) {
	if service == nil {
		http.Error(w, "document service is not configured", http.StatusServiceUnavailable)
		return
	}
	document, err := service.CreateDocument(r.Context())
	if err != nil {
		http.Error(w, "could not create document", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(struct {
		Slug string `json:"slug"`
	}{Slug: document.Slug})
}

func handleEditorPage(w http.ResponseWriter, r *http.Request, service DocumentService) {
	slug := r.PathValue("slug")
	if !documentSlugPattern.MatchString(slug) || service == nil {
		http.NotFound(w, r)
		return
	}
	if _, err := service.LoadDocument(r.Context(), slug); err != nil {
		if errors.Is(err, store.ErrDocumentNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "could not load document", http.StatusInternalServerError)
		return
	}
	servePage(w, "editor.html")
}

func servePage(w http.ResponseWriter, name string) {
	data, err := fs.ReadFile(web.FS, name)
	if err != nil {
		http.Error(w, "page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}
