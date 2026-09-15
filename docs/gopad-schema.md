# Gopad — Database Schema (SQLite)

Covers `users`, `documents`, and `operations`, plus how they interact with the WebSocket connect handshake.

## 1. Users

Identity in v1 is username-only: no passwords, no verification. A username is a claim, not an authenticated identity — anyone who types an existing username "becomes" that user. This is an intentional, explicit v1 trust gap, not an oversight, and matches the project's "no auth" non-goal — this schema just makes that concrete.

```sql
CREATE TABLE users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    username   TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color      TEXT NOT NULL,          -- assigned once at creation, stable across sessions
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Notes:
- `UNIQUE COLLATE NOCASE` — "Alice" and "alice" collide (deliberate: avoids confusing near-duplicate identities).
- Usernames are **globally unique across all of Gopad**, not scoped per-document. Simpler schema, and a more realistic rehearsal of how this would work as a real product.
- `color` is assigned once at creation and stored — not re-randomized per session — so a returning user shows the same presence color every time.

## 2. Documents

```sql
CREATE TABLE documents (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    slug             TEXT NOT NULL UNIQUE,       -- random shareable ID, e.g. base62, 10-12 chars
    title            TEXT,
    snapshot         TEXT NOT NULL DEFAULT '',   -- materialized CRDT state, serialized as JSON
    snapshot_version INTEGER NOT NULL DEFAULT 0, -- op `sequence` this snapshot reflects
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Notes:
- `snapshot` stores the **full serialized CRDT structure** (the list of `Char`s including tombstones, as JSON) — not just the plain-text rendering. This lets the server rebuild an in-memory canonical CRDT for a room on startup without replaying the entire op history from the beginning.
- `snapshot_version` marks how far the snapshot covers. On room startup: load `snapshot`, then replay all `operations` where `sequence > snapshot_version`.
- `slug` is randomly generated, not sequential or guessable — it is the entire access-control mechanism in v1 (anyone with the link can view/edit).

## 3. Operations

```sql
CREATE TABLE operations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER NOT NULL REFERENCES documents(id),
    sequence    INTEGER NOT NULL,     -- server-assigned, monotonic per document
    user_id     INTEGER NOT NULL REFERENCES users(id),
    site_id     TEXT NOT NULL,        -- CRDT siteID used for this op's CharIDs (per connection, not per user)
    op_type     TEXT NOT NULL,        -- 'insert' | 'delete'
    payload     TEXT NOT NULL,        -- JSON: CharID/value/leftID/rightID (insert) or target CharID (delete)
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

    UNIQUE(document_id, sequence)
);

CREATE INDEX idx_operations_document_sequence ON operations(document_id, sequence);
```

Notes:
- **Two distinct orderings, not to be conflated**:
  - `sequence` — server-assigned, durable record order, per document. Used for replay/catch-up ("give me everything after sequence N").
  - The CRDT's own `(counter, siteID)` comparator (inside `payload`) — defines merge/document order. The server doesn't need to understand this to relay/persist ops; it only matters when replaying into an in-memory CRDT.
- **`user_id` vs `site_id`** — intentionally kept separate:
  - `user_id`: which human made this edit (attribution, presence display).
  - `site_id`: which CRDT replica (browser tab/session) generated this CharID — required for CRDT merge correctness, irrelevant to attribution.
  - A single user could have multiple `site_id`s (multiple open tabs). Never collapse these into one field.
- `payload` is stored as JSON for v1 simplicity. Could be normalized into columns later (`char_site_id`, `char_counter`, `value`, `left_site_id`, `left_counter`) for queryability, but JSON matches how it's deserialized before being applied to the in-memory CRDT, so there's no real win in normalizing yet.

## 4. Connect handshake (how these tables meet the WebSocket layer)

1. Client has (or prompts for) a username on first visit; stored in `localStorage` so returning visitors aren't re-prompted.
2. On connecting to `/d/{slug}`, client sends `{ username }` (as the first WS message, or a lightweight pre-flight `POST /session`).
3. Server does **find-or-create** on `users` by username, returns `user_id` + `color`.
4. Server generates a fresh `site_id` for this connection. This is **never persisted in `users`** — it's ephemeral, scoped to the lifetime of that tab's CRDT replica, and only ever shows up in `operations.site_id` and the in-memory `Room` client registry.
5. Server replies with `{ user_id, site_id, color, snapshot }` — this response doubles as the `sync` message defined in the WebSocket protocol.

## 5. Room startup / recovery (snapshot + replay)

When a document's room is first activated (first client connects after server start, or after all clients left and state was evicted from memory):

1. Load `documents.snapshot` (JSON) and `documents.snapshot_version`.
2. Deserialize into the in-memory canonical CRDT structure for that room.
3. Query `operations WHERE document_id = ? AND sequence > snapshot_version ORDER BY sequence`.
4. Apply each op to the in-memory CRDT, in `sequence` order.
5. Room is now caught up; ready to accept new connections and live ops.

Snapshotting policy (when to write a new `documents.snapshot`) is still an open question — candidates: every N ops, every X seconds of activity, or on last-client-disconnect. Not yet decided.
