package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/gopad/internal/cache"
	"github.com/local/gopad/internal/store"
)

func TestCreateDocument_ReturnsSlug(t *testing.T) {
	database := openServerTestStore(t)
	request := httptest.NewRequest(http.MethodPost, "/documents", nil)
	recorder := httptest.NewRecorder()

	New(database).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusCreated)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content type = %q, want application/json", contentType)
	}
	var response struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9A-Za-z]{12}$`).MatchString(response.Slug) {
		t.Fatalf("slug = %q, want 12-character base62", response.Slug)
	}
}

func TestStaticRoutes_ServeLandingEditorAndAssets(t *testing.T) {
	database := openServerTestStore(t)
	document, err := database.CreateDocument(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(database)

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "landing", path: "/", want: "New document"},
		{name: "editor", path: "/d/" + document.Slug, want: "Gopad editor"},
		{name: "style", path: "/assets/style.css", want: ".page-shell"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}
			body, err := io.ReadAll(recorder.Result().Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), test.want) {
				t.Fatalf("body does not contain %q", test.want)
			}
		})
	}
}

func TestDocumentRoute_RejectsMalformedAndUnknownSlugs(t *testing.T) {
	database := openServerTestStore(t)
	handler := New(database)

	for _, slug := range []string{"short", "not-a-valid!", "abcdefghijkl"} {
		request := httptest.NewRequest(http.MethodGet, "/d/"+slug, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Errorf("slug %q: status = %d, want %d", slug, recorder.Code, http.StatusNotFound)
		}
	}
}

type countingDocumentService struct {
	mu       sync.Mutex
	document store.LoadedDocument
	loads    int
}

func (service *countingDocumentService) CreateDocument(context.Context) (store.Document, error) {
	return service.document.Document, nil
}

func (service *countingDocumentService) LoadDocument(_ context.Context, slug string) (store.LoadedDocument, error) {
	service.mu.Lock()
	service.loads++
	service.mu.Unlock()
	if slug != service.document.Slug {
		return store.LoadedDocument{}, store.ErrDocumentNotFound
	}
	return service.document, nil
}

func (service *countingDocumentService) loadCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.loads
}

func TestDocumentRouteCachesSlugExistence(t *testing.T) {
	service := &countingDocumentService{document: store.LoadedDocument{Document: store.Document{Slug: "Abc123456789"}}}
	slugCache := cache.NewSlugCache(time.Minute, time.Minute, 8)
	defer slugCache.Close()
	handler := New(service, slugCache)

	for range 2 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/d/Abc123456789", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("existing document status = %d, want %d", recorder.Code, http.StatusOK)
		}
	}
	if service.loadCount() != 1 {
		t.Fatalf("existing slug loads = %d, want 1", service.loadCount())
	}

	for range 2 {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/d/Zzz123456789", nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("missing document status = %d, want %d", recorder.Code, http.StatusNotFound)
		}
	}
	if service.loadCount() != 2 {
		t.Fatalf("missing slug loads = %d, want one cached miss", service.loadCount())
	}
}

func openServerTestStore(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "gopad.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return database
}
