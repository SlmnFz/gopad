package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/local/gopad/internal/crdt"
)

func TestOpen_EnablesWALAndCreatesSchema(t *testing.T) {
	store := openTestStore(t)

	var journalMode string
	if err := store.readDB.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}

	var tableCount int
	if err := store.readDB.QueryRowContext(context.Background(), `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name IN ('users', 'documents', 'operations')
	`).Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 3 {
		t.Fatalf("schema table count = %d, want 3", tableCount)
	}
}

func TestCreateDocument_GeneratesUniqueBase62Slugs(t *testing.T) {
	store := openTestStore(t)
	pattern := regexp.MustCompile(`^[0-9A-Za-z]{12}$`)
	seen := make(map[string]struct{})

	for range 25 {
		document, err := store.CreateDocument(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(document.Slug) {
			t.Fatalf("slug %q does not match base62 format", document.Slug)
		}
		if _, exists := seen[document.Slug]; exists {
			t.Fatalf("duplicate slug %q", document.Slug)
		}
		seen[document.Slug] = struct{}{}
	}
}

func TestStore_RecoversSnapshotAndPostSnapshotOperations(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "gopad.db")
	store, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}

	document, err := store.CreateDocument(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := crdt.CharID{SiteID: "site-a", Counter: 1}
	b := crdt.CharID{SiteID: "site-a", Counter: 2}
	first := crdt.Operation{Type: crdt.Insert, ID: a, Value: 'A'}
	second := crdt.Operation{Type: crdt.Insert, ID: b, Value: 'B', LeftID: &a}

	if err := store.AppendOperations(context.Background(), document.ID, []crdt.Operation{first}); err != nil {
		t.Fatal(err)
	}
	snapshotDocument := crdt.New()
	if err := snapshotDocument.Apply(first); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshot(context.Background(), document.ID, snapshotDocument.Snapshot(), 1); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendOperations(context.Background(), document.ID, []crdt.Operation{second}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadDocument(context.Background(), document.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SnapshotVersion != 1 {
		t.Fatalf("snapshot version = %d, want 1", loaded.SnapshotVersion)
	}
	if len(loaded.Operations) != 1 || loaded.Operations[0].Sequence != 2 {
		t.Fatalf("operations after snapshot = %#v, want sequence 2", loaded.Operations)
	}

	recovered := crdt.New()
	for _, char := range loaded.Snapshot {
		operation := crdt.Operation{Type: crdt.Insert, ID: char.ID, Value: char.Value, LeftID: char.LeftID}
		if err := recovered.Apply(operation); err != nil {
			t.Fatal(err)
		}
		if char.Deleted {
			if err := recovered.Apply(crdt.Operation{Type: crdt.Delete, ID: char.ID}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, record := range loaded.Operations {
		if err := recovered.Apply(record.Operation); err != nil {
			t.Fatal(err)
		}
	}
	if got := recovered.Text(); got != "AB" {
		t.Fatalf("recovered text = %q, want %q", got, "AB")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStore_LoadMissingDocument(t *testing.T) {
	store := openTestStore(t)
	_, err := store.LoadDocument(context.Background(), "missing")
	if !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("error = %v, want document not found", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "gopad.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}
