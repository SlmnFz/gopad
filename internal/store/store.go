package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/local/gopad/internal/crdt"
	_ "modernc.org/sqlite"
)

const (
	slugLength         = 12
	slugAlphabet       = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	systemUserID int64 = 1
)

var userColors = []string{
	"#4dd8c0",
	"#7aa2f7",
	"#bb9af7",
	"#f7768e",
	"#e0af68",
	"#9ece6a",
	"#7dcfff",
	"#ff9e64",
}

//go:embed migrations/001_initial.sql
var migrationFS embed.FS

var (
	ErrDocumentNotFound = errors.New("document not found")
)

// Store owns separate read and single-connection write handles. SQLite can
// serve readers concurrently in WAL mode, while all writes are serialized by
// the write handle and its caller.
type Store struct {
	readDB  *sql.DB
	writeDB *sql.DB
}

// Document is the persisted document metadata and latest CRDT snapshot.
type Document struct {
	ID              int64
	Slug            string
	Title           string
	Snapshot        []crdt.Char
	SnapshotVersion int64
}

// User is the username-only identity shown in presence and attached to edits.
type User struct {
	ID       int64
	Username string
	Color    string
}

// OperationRecord is one persisted operation after a document snapshot.
type OperationRecord struct {
	Sequence  int64
	UserID    int64
	SiteID    string
	Operation crdt.Operation
}

// LoadedDocument contains the snapshot and operations that must be replayed
// after that snapshot version.
type LoadedDocument struct {
	Document
	Operations []OperationRecord
}

// Open opens or creates a SQLite database and applies the initial schema.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if path == ":memory:" {
		path = "file:gopad-memory?mode=memory&cache=shared"
	}

	writeDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open write database: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	readDB, err := sql.Open("sqlite", path)
	if err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("open read database: %w", err)
	}

	store := &Store{readDB: readDB, writeDB: writeDB}
	if err := configureDatabase(context.Background(), writeDB, true); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := applyMigrations(context.Background(), writeDB); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := configureDatabase(context.Background(), readDB, false); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

func configureDatabase(ctx context.Context, db *sql.DB, writer bool) error {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("configure busy timeout: %w", err)
	}
	if writer {
		var journalMode string
		if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&journalMode); err != nil {
			return fmt.Errorf("enable WAL mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return fmt.Errorf("enable WAL mode: got %q", journalMode)
		}
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	return nil
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	migration, err := migrationFS.ReadFile("migrations/001_initial.sql")
	if err != nil {
		return fmt.Errorf("read initial migration: %w", err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		return fmt.Errorf("apply initial migration: %w", err)
	}
	return nil
}

// Close closes both database handles.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.readDB != nil {
		if err := s.readDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.writeDB != nil {
		if err := s.writeDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// FindOrCreateUser returns the globally unique, case-insensitive username
// record. Colors are assigned once and remain stable across sessions.
func (s *Store) FindOrCreateUser(ctx context.Context, username string) (User, error) {
	if s == nil || s.writeDB == nil {
		return User{}, errors.New("store is not open")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		username = "Anonymous"
	}
	if len([]rune(username)) > 64 {
		return User{}, errors.New("username cannot exceed 64 characters")
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin user transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var user User
	err = tx.QueryRowContext(ctx, `
		SELECT id, username, color
		FROM users
		WHERE username = ?
	`, username).Scan(&user.ID, &user.Username, &user.Color)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("find user: %w", err)
	}

	var userCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&userCount); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	user = User{Username: username, Color: userColors[(userCount-1)%len(userColors)]}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, color)
		VALUES (?, ?)
	`, user.Username, user.Color)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	user.ID, err = result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read user ID: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit user: %w", err)
	}
	return user, nil
}

// CreateDocument creates a document with a cryptographically random base62
// slug. Collisions are retried because the slug is the v1 access mechanism.
func (s *Store) CreateDocument(ctx context.Context) (Document, error) {
	if s == nil || s.writeDB == nil {
		return Document{}, errors.New("store is not open")
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, fmt.Errorf("begin document transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for range 10 {
		slug, err := randomSlug()
		if err != nil {
			return Document{}, err
		}
		result, err := tx.ExecContext(ctx, `
			INSERT INTO documents (slug, snapshot, snapshot_version)
			VALUES (?, '', 0)
		`, slug)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return Document{}, fmt.Errorf("insert document: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return Document{}, fmt.Errorf("read document ID: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Document{}, fmt.Errorf("commit document: %w", err)
		}
		return Document{ID: id, Slug: slug, Snapshot: []crdt.Char{}}, nil
	}
	return Document{}, errors.New("could not generate a unique document slug")
}

// AppendOperations stores a batch and assigns monotonically increasing
// per-document server sequence numbers. Task 7 will add authenticated user
// attribution; until then operations use the seeded system user.
func (s *Store) AppendOperations(ctx context.Context, documentID int64, operations []crdt.Operation) error {
	return s.appendOperations(ctx, documentID, systemUserID, operations)
}

// AppendOperationsForUser stores operations with the connected user's
// attribution while preserving the CRDT site ID from each operation.
func (s *Store) AppendOperationsForUser(ctx context.Context, documentID, userID int64, operations []crdt.Operation) error {
	return s.appendOperations(ctx, documentID, userID, operations)
}

func (s *Store) appendOperations(ctx context.Context, documentID, userID int64, operations []crdt.Operation) error {
	if len(operations) == 0 {
		return nil
	}
	if s == nil || s.writeDB == nil {
		return errors.New("store is not open")
	}
	if userID <= 0 {
		userID = systemUserID
	}

	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin operation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sequence int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0)
		FROM operations
		WHERE document_id = ?
	`, documentID).Scan(&sequence); err != nil {
		return fmt.Errorf("read operation sequence: %w", err)
	}

	for _, operation := range operations {
		if operation.Type != crdt.Insert && operation.Type != crdt.Delete {
			return fmt.Errorf("unsupported operation type %q", operation.Type)
		}
		payload, err := json.Marshal(operation)
		if err != nil {
			return fmt.Errorf("marshal operation: %w", err)
		}
		sequence++
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO operations (document_id, sequence, user_id, site_id, op_type, payload)
			VALUES (?, ?, ?, ?, ?, ?)
		`, documentID, sequence, systemUserID, operation.ID.SiteID, operation.Type, payload); err != nil {
			return fmt.Errorf("insert operation %d: %w", sequence, err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE documents
		SET updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, documentID)
	if err != nil {
		return fmt.Errorf("update document timestamp: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check document update: %w", err)
	} else if rows != 1 {
		return fmt.Errorf("document %d: %w", documentID, ErrDocumentNotFound)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit operations: %w", err)
	}
	return nil
}

// SaveSnapshot persists the full CRDT structure and the operation sequence it
// covers. The full structure includes tombstones for future references.
func (s *Store) SaveSnapshot(ctx context.Context, documentID int64, chars []crdt.Char, snapshotVersion int64) error {
	if s == nil || s.writeDB == nil {
		return errors.New("store is not open")
	}
	if snapshotVersion < 0 {
		return errors.New("snapshot version cannot be negative")
	}
	if chars == nil {
		chars = []crdt.Char{}
	}
	snapshot, err := json.Marshal(chars)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	result, err := s.writeDB.ExecContext(ctx, `
		UPDATE documents
		SET snapshot = ?, snapshot_version = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, snapshot, snapshotVersion, documentID)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check snapshot update: %w", err)
	} else if rows != 1 {
		return fmt.Errorf("document %d: %w", documentID, ErrDocumentNotFound)
	}
	return nil
}

// LoadDocument loads a document snapshot and all operations after its
// snapshot version, ordered by the server-assigned sequence.
func (s *Store) LoadDocument(ctx context.Context, slug string) (LoadedDocument, error) {
	if s == nil || s.readDB == nil {
		return LoadedDocument{}, errors.New("store is not open")
	}

	var (
		document     Document
		title        sql.NullString
		snapshotJSON string
	)
	err := s.readDB.QueryRowContext(ctx, `
		SELECT id, slug, title, snapshot, snapshot_version
		FROM documents
		WHERE slug = ?
	`, slug).Scan(&document.ID, &document.Slug, &title, &snapshotJSON, &document.SnapshotVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return LoadedDocument{}, fmt.Errorf("slug %q: %w", slug, ErrDocumentNotFound)
	}
	if err != nil {
		return LoadedDocument{}, fmt.Errorf("load document: %w", err)
	}
	document.Title = title.String
	if strings.TrimSpace(snapshotJSON) == "" {
		document.Snapshot = []crdt.Char{}
	} else if err := json.Unmarshal([]byte(snapshotJSON), &document.Snapshot); err != nil {
		return LoadedDocument{}, fmt.Errorf("decode snapshot: %w", err)
	}
	if document.Snapshot == nil {
		document.Snapshot = []crdt.Char{}
	}

	rows, err := s.readDB.QueryContext(ctx, `
		SELECT sequence, user_id, site_id, op_type, payload
		FROM operations
		WHERE document_id = ? AND sequence > ?
		ORDER BY sequence ASC
	`, document.ID, document.SnapshotVersion)
	if err != nil {
		return LoadedDocument{}, fmt.Errorf("load operations: %w", err)
	}
	defer rows.Close()

	loaded := LoadedDocument{Document: document, Operations: []OperationRecord{}}
	for rows.Next() {
		var (
			record  OperationRecord
			opType  string
			payload string
		)
		if err := rows.Scan(&record.Sequence, &record.UserID, &record.SiteID, &opType, &payload); err != nil {
			return LoadedDocument{}, fmt.Errorf("scan operation: %w", err)
		}
		if err := json.Unmarshal([]byte(payload), &record.Operation); err != nil {
			return LoadedDocument{}, fmt.Errorf("decode operation %d: %w", record.Sequence, err)
		}
		if record.Operation.Type == "" {
			record.Operation.Type = crdt.OperationType(opType)
		}
		loaded.Operations = append(loaded.Operations, record)
	}
	if err := rows.Err(); err != nil {
		return LoadedDocument{}, fmt.Errorf("read operations: %w", err)
	}
	return loaded, nil
}

func randomSlug() (string, error) {
	result := make([]byte, slugLength)
	randomBytes := make([]byte, slugLength)
	const limit = 256 - (256 % len(slugAlphabet))
	filled := 0
	for filled < len(result) {
		if _, err := rand.Read(randomBytes); err != nil {
			return "", fmt.Errorf("generate document slug: %w", err)
		}
		for _, randomByte := range randomBytes {
			if int(randomByte) >= limit {
				continue
			}
			result[filled] = slugAlphabet[int(randomByte)%len(slugAlphabet)]
			filled++
			if filled == len(result) {
				break
			}
		}
	}
	return string(result), nil
}
