# Gopad — Project Spec

A minimal Google-Docs-style collaborative text editor, built to learn how real-time collaborative editing (CRDTs, WebSockets, presence) actually works under the hood.

## 1. Overview

Users create a document, get a random shareable link, and anyone with that link can join and edit concurrently with live presence (cursors, who's online). Conflict resolution is handled client-side via a CRDT; the Go server relays, sequences, and persists operations.

## 2. Tech stack

| Layer | Choice |
|---|---|
| Server language | Go |
| Realtime transport | `github.com/coder/websocket` |
| Persistence | SQLite |
| Frontend | Plain HTML + vanilla JS (single static file, no build step) |
| Conflict resolution | Sequence CRDT (RGA-style — unique per-character IDs, tombstone deletes) |

## 3. Core features

### 3.1 Document creation & sharing
- `POST /documents` → creates a new empty document, returns a random unguessable slug/ID (e.g. 10–12 char base62 string).
- Visiting `/d/{slug}` opens that document in the editor.
- No auth for v1 — anyone with the link can view and edit (matches "anyone with the link can edit" mode in Google Docs). Auth/permissions explicitly out of scope for v1.

### 3.2 Realtime editing
- Client maintains a local CRDT replica of the document.
- Keystrokes → local CRDT ops → applied locally immediately (optimistic) → sent to server over WebSocket.
- Server receives op, assigns/stores a sequence number, persists it, broadcasts to all other connected clients in that document's room.
- Remote clients receive the op, apply it to their local CRDT, re-render.
- Late joiners: server sends current full document snapshot (materialized from CRDT state) on connect, before streaming live ops.

### 3.3 Presence
- Each client broadcasts:
  - **Cursor/selection position** — frequent, ephemeral, not persisted, not ordered/reliable (last one wins, drops are fine).
  - **Join/leave events** — who's currently viewing/editing the doc.
  - **Identity** — for v1, an ephemeral display name + assigned color (no accounts), generated client-side or assigned by server on connect.
- Presence is rendered as: colored cursor + name label per active collaborator, and a "N people editing" indicator.

### 3.4 Persistence
- SQLite stores:
  - `documents`: id/slug, title (optional), created_at, current snapshot (materialized text or serialized CRDT state), snapshot_version.
  - `operations`: id, document_id, site_id (which client/session made the op), sequence number, op payload, timestamp.
- Snapshotting strategy: periodically (e.g. every N ops or every X seconds) materialize the CRDT state and store it, so recovery/late-join doesn't require replaying the entire op history from op #1 forever.
- Server survives restart: documents and their full edit history are not lost.

### 3.5 Connection resilience
- Client reconnects automatically on WebSocket drop (with backoff).
- On reconnect, client re-syncs by re-fetching the current snapshot and resuming the live op stream (simpler than replaying unacked local ops — chosen for v1).
- Server cleans up in-memory room state when the last client disconnects (TTL-based cleanup is a later refinement, not a v1 blocker).

## 4. Frontend (plain HTML + vanilla JS)

No build step, no framework — a small number of static files served directly by the Go server. Keeps focus on the CRDT/networking logic rather than frontend tooling.

- **Files**: `index.html` (landing/create page), `editor.html` (or a single `index.html` that branches on path), `crdt.js` (the local CRDT implementation), `ws.js` (connection lifecycle), `app.js` (wires DOM ↔ CRDT ↔ socket). Plain `<script>` tags, no bundler.
- **Editor view**: a `textarea` (simplest, least surprising cursor/selection behavior) bound to the local CRDT state. `contenteditable` is a possible later upgrade once plain-text editing works, since it brings its own set of quirks.
- **Local CRDT module** (`crdt.js`): a JS implementation of the same RGA-style structure the server persists — this is the client's source of truth for rendering, independent of network state.
- **WebSocket module** (`ws.js`): manages connection lifecycle, reconnect/backoff, and dispatches incoming messages (`op` / `cursor` / `presence` / `sync`) into the CRDT and presence state.
- **Presence layer**: renders remote cursors (colored carets + name tags) as an overlay, positioned from character IDs (not raw offsets, so they stay correct as the document changes).
- **Doc creation page**: simple "New Document" button hitting `POST /documents`, redirecting to `/d/{slug}`.
- **Routing**: no client-side router needed — `/` (create/landing) and `/d/{slug}` (editor) are just two server-served HTML pages.

## 5. Message envelope (WebSocket protocol)

```json
{
  "type": "op | cursor | presence | sync",
  "payload": { }
}
```

| Type | Persisted? | Ordering/reliability | Notes |
|---|---|---|---|
| `op` | Yes (operations table) | Server-sequenced | CRDT insert/delete |
| `cursor` | No | Best-effort, lossy OK | High frequency |
| `presence` | No | Best-effort | Join/leave/name/color |
| `sync` | N/A (read from snapshot) | Sent once on connect | Full doc state + active presence list |

## 6. Explicit non-goals (v1)

- No authentication / accounts / permissions.
- No rich text formatting (bold/italic/headings) — plain text only. (Rich text CRDT is a significant additional complexity layer — good v2 stretch goal.)
- No document listing/dashboard — you get a doc via direct link only, no "my documents" page.
- No offline editing support (CRDT makes this *possible* later, but sync-on-reconnect logic for long offline periods is extra work).
- No undo/redo (interacts with CRDTs in non-trivial ways — worth a dedicated design pass later).
- No rate limiting / abuse protection — fine for a local learning project.

## 7. Milestones (build order)

1. **Skeleton**: create doc → get slug → two browser tabs connect via WebSocket to that room → dumb full-text broadcast (no CRDT yet, last-write-wins). Validates hub/room/connection plumbing.
2. **CRDT integration**: replace dumb broadcast with real CRDT ops (insert/delete with unique IDs), client-side merge logic in both the Go relay/persistence layer and the React client. Validates conflict resolution.
3. **Persistence**: SQLite tables, op logging, periodic snapshotting, load-on-connect.
4. **Presence**: cursors, colors, names, join/leave indicators — separate lightweight channel, no persistence.
5. **Resilience**: reconnect/backoff, resync on reconnect.
6. **(Stretch)** Rich text support, undo/redo, OT alternative implementation for comparison, auth.

## 8. CRDT design — character IDs (RGA-style)

Gopad uses a sequence CRDT modeled on RGA (Replicated Growable Array) / WOOT-family designs: every character gets a permanent, globally unique, totally ordered identifier that never changes, so "insert after character X" always means the same thing regardless of concurrent edits elsewhere in the document.

### 8.1 Data model

```
CharID = {
  siteID:  string   // random UUID (or short ID) assigned per browser session
  counter: uint64   // monotonically increasing, per-site, starts at 1
}

Char = {
  id:      CharID
  value:   rune
  leftID:  CharID | nil   // ID of the character this was inserted after (nil = start of doc)
  rightID: CharID | nil   // ID of the character this was inserted before (nil = end of doc)
  deleted: bool            // tombstone flag
}
```

- Each client (site) generates a unique `siteID` on connect.
- Every character that site inserts gets `(siteID, counter)`, with `counter` incrementing locally per site — this guarantees global uniqueness without coordination.
- The document is a constraint-ordered sequence of `Char`s. `leftID` preserves the
  insertion's lower bound and `rightID` preserves its upper bound; a nil `rightID`
  means the insertion is at the end of the current sequence. Rendering the visible
  string walks the resulting sequence and skips tombstones.

### 8.2 Delete = tombstone, not removal

Deletes initially set `deleted: true` rather than removing a character. This preserves position references for concurrent operations that might still reference that character as a `leftID` or `rightID`. After a successful snapshot, the writer may ask the room owner to compact tombstones that were already present in the previous successful snapshot. That one-snapshot grace window is a practical latency heuristic, not a causal-stability proof; the durable operation log is retained even when the in-memory CRDT structure is compacted.

### 8.3 Ordering / tiebreak rule

When two characters are inserted concurrently in the same gap (same `leftID` and
`rightID`), all replicas must resolve the tie identically. The deterministic base
ordering uses `counter` first, then `siteID` as a fixed tiebreak; the adjacent
anchors then constrain each new character to remain in its observed gap. Since
every replica applies the same constraints to the same set of characters, all
replicas converge on an identical linear order regardless of operation arrival
order.

### 8.4 Wire protocol (operations)

```json
// insert
{ "type": "insert", "id": { "siteID": "...", "counter": 5 }, "value": "x", "leftID": { "siteID": "...", "counter": 4 }, "rightID": { "siteID": "...", "counter": 6 } }

// delete
{ "type": "delete", "id": { "siteID": "...", "counter": 5 } }
```

These are the payloads carried inside the `op` envelope type from the message protocol (section 5).

### 8.5 Client-side bookkeeping

The client needs two synchronized views of the document:
- **Full list**: every `Char` ever inserted, including tombstones, ordered per section 8.3 — this is the actual CRDT state, used to compute adjacent anchors when the local user types at a given cursor position.
- **Visible view**: the derived string with tombstones filtered out — this is what actually renders in the editor.

When the local user inserts at screen-position N, the client resolves N to the
characters currently at visible positions N-1 and N via the visible view, then
emits an `insert` op referencing them as `leftID` and `rightID`. This prevents a
local middle insertion from being reordered after an already-existing sibling
whose ID happens to sort earlier.

### 8.6 Why this approach (vs. fractional indexing)

Considered fractional indexing (Logoot-style: numeric positions that sort between neighbors, e.g. inserting "15" between "10" and "20") as an alternative. Chose the RGA/`(siteID, counter)` approach for v1 because:
- It's the more "classical" CRDT design (closely matches the RGA/WOOT literature), so the mental model transfers directly to further reading.
- The tombstone-based delete is a concrete, hands-on lesson in the core CRDT tradeoff (correctness vs. unbounded growth).
- Fractional indexing is a good candidate for a v2 exploration once RGA is well understood — its interleaving-avoidance tricks are more appreciated after feeling RGA's limitations firsthand.

## 9. Open design questions to resolve before coding

- CRDT character-ID scheme: `(siteID, counter)` pairs vs. fractional indexing vs. Logoot-style identifiers — needs its own design pass.
- Exact per-type payload schemas (fields for `op`, `cursor`, `presence`, `sync`).
- Snapshot trigger policy (every N ops? every N seconds? on last-client-disconnect?).
- Whether the Go server keeps a canonical in-memory CRDT per active room (recommended — makes late-joiner sync and snapshotting trivial) or relays blindly and reconstructs from the op log on demand (simpler server, slower late-join).
- Whether the React CRDT implementation is hand-written (better for learning) or a thin wrapper reusing an existing library like Yjs (faster to get a working demo, less educational).
