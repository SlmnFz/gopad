PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    username   TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color      TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS documents (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    slug             TEXT NOT NULL UNIQUE,
    title            TEXT,
    snapshot         TEXT NOT NULL DEFAULT '',
    snapshot_version INTEGER NOT NULL DEFAULT 0,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS operations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER NOT NULL REFERENCES documents(id),
    sequence    INTEGER NOT NULL,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    site_id     TEXT NOT NULL,
    op_type     TEXT NOT NULL CHECK (op_type IN ('insert', 'delete')),
    payload     TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(document_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_operations_document_sequence
    ON operations(document_id, sequence);

INSERT OR IGNORE INTO users (id, username, color)
VALUES (1, '__system__', '#808080');
