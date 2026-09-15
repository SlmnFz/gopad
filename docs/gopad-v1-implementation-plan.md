# Gopad v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Each task builds a working, verifiable increment — implement first, then verify with tests, not test-first.

**Goal:** Build a single-node collaborative plain-text editor with shareable documents, an RGA-style CRDT, SQLite recovery, live presence, and operational instrumentation.

**Build approach:** Tasks are built in the correct final order (CRDT → persistence → HTTP → realtime rooms → browser client → presence → batching/observability), not as a throwaway naive version replaced later. Task 1 and Task 5's transport-only tests validate the networking/concurrency plumbing before CRDT complexity is layered on, so there's no separate "dumb broadcast" milestone to build and discard. Each task: implement the increment, write tests that verify its actual behavior, run and confirm, then commit.

**Architecture:** A Go HTTP server owns active document rooms and a canonical CRDT per room. Browser replicas apply operations optimistically and exchange typed JSON envelopes over WebSockets; one serialized SQLite writer persists operation batches and snapshots off the room event loop.

**Tech Stack:** Go 1.22+, `net/http`, `github.com/coder/websocket`, `modernc.org/sqlite` (pure Go, no cgo — simpler cross-compilation, acceptable perf tradeoff for this project's scale), vanilla HTML/CSS/JavaScript, Prometheus client, k6 with WebSocket support.

**Spec:** `gopad-spec.md` (with persistence details in `gopad-schema.md` and concurrency/scaling details in `gopad-architecture.md`)

## Global Constraints

- v1 is plain text, link-shared, and intentionally has no authentication or permissions.
- Character IDs are `(siteID, counter)` pairs; concurrent siblings sort by counter, then site ID.
- Deletes create tombstones; they never remove referenced characters.
- Persist operations with a monotonic per-document server sequence distinct from CRDT order.
- Keep blocking I/O and fan-out work outside the room state-mutation loop.
- Room fan-out does not wait for delivery to complete before the event loop continues — CRDT correctness is order-independent, so per-client ordering is left to each client's own buffered FIFO send channel (see `gopad-architecture.md` §9.1). Do not add a wait/join on the fan-out pool.
- Document slugs are exactly 12 characters, `[0-9A-Za-z]{12}`, generated with `crypto/rand`. This is the sole access-control mechanism in v1 — never make slugs sequential or otherwise guessable.
- Serve static browser assets directly; do not add a frontend build system.

---

### Task 1: Server foundation and health endpoint

**Files:**
- Create: `go.mod`
- Create: `cmd/gopad/main.go`
- Create: `internal/server/server.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Produces: `server.New() http.Handler`

- [x] **Step 1: Initialize the module**

Run: `go mod init github.com/local/gopad`

- [x] **Step 2: Implement the minimal server**

```go
func New() http.Handler {
    mux := http.NewServeMux()
    mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        _, _ = io.WriteString(w, "ok\n")
    })
    return mux
}
```

Wire `main.go` to `http.Server{Addr: ":8080", Handler: server.New()}` with signal-aware graceful shutdown (listen for `SIGINT`/`SIGTERM`, call `Shutdown(ctx)` with a bounded timeout).

- [x] **Step 3: Verify it runs**

Run: `go run ./cmd/gopad` and confirm `curl localhost:8080/healthz` returns `ok`.

- [x] **Step 4: Add tests covering the health endpoint**

```go
func TestHealthz_ReturnsOK(t *testing.T) {
    req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
    rec := httptest.NewRecorder()
    New().ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
        t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
    }
}
```

- [x] **Step 5: Format and verify**

Run: `gofmt -w cmd internal && go test ./... && go vet ./...`
Expected: all commands pass.

- [x] **Step 6: Commit**

```bash
git add go.mod cmd/gopad/main.go internal/server
git commit -m "feat(server): add HTTP service foundation"
```

### Task 2: CRDT operation model and convergence

**Files:**
- Create: `internal/crdt/document.go`
- Test: `internal/crdt/document_test.go`

**Interfaces:**
- Produces: `CharID`, `Operation`, `New() *Document`, `(*Document).Apply(Operation) error`, `(*Document).Text() string`, `(*Document).Snapshot() []Char`

- [x] **Step 1: Implement the RGA document**

Store characters by ID, retain `LeftID`, sort siblings deterministically with `(Counter, SiteID)`, recursively emit descendants for traversal, and skip deleted values only in `Text()` (tombstones stay in the underlying structure — deletes flag, never remove, per Global Constraints). Reject conflicting duplicate IDs and deletes of unknown IDs with typed errors rather than panicking.

- [x] **Step 2: Add convergence and edge-case tests**

```go
func TestDocument_ConcurrentSiblingsConverge(t *testing.T) {
    a := Operation{Type: Insert, ID: CharID{SiteID: "a", Counter: 1}, Value: 'A'}
    b := Operation{Type: Insert, ID: CharID{SiteID: "b", Counter: 1}, Value: 'B'}
    first, second := New(), New()
    for _, op := range []Operation{a, b} { if err := first.Apply(op); err != nil { t.Fatal(err) } }
    for _, op := range []Operation{b, a} { if err := second.Apply(op); err != nil { t.Fatal(err) } }
    if first.Text() != second.Text() || first.Text() != "AB" { t.Fatalf("%q != %q", first.Text(), second.Text()) }
}
```

Add cases for insert-after, delete tombstones, duplicate idempotence, missing parents, and Unicode runes.

- [x] **Step 3: Verify convergence and races**

Run: `go test -race ./internal/crdt -v`
Expected: PASS.

Regular CRDT tests and `go vet ./...` pass locally. The GitHub Actions run passed the race-enabled suite on Ubuntu with CGO enabled.

- [x] **Step 4: Commit**

```bash
git add internal/crdt
git commit -m "feat(crdt): add convergent RGA document"
```

### Task 3: SQLite schema, document creation, and recovery

**Files:**
- Create: `internal/store/migrations/001_initial.sql`
- Create: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Consumes: `crdt.Operation`, `crdt.Char`
- Produces: `Open(path string) (*Store, error)`, `CreateDocument(context.Context) (Document, error)`, `AppendOperations(context.Context, int64, []crdt.Operation) error`, `SaveSnapshot(context.Context, int64, []crdt.Char, int64) error`, `LoadDocument(context.Context, string) (LoadedDocument, error)`

- [x] **Step 1: Implement schema and store**

Translate the three tables and index from `gopad-schema.md` (`users`, `documents`, `operations`) into the migration file. Use separate read/write `*sql.DB` handles per `gopad-architecture.md` §9.3: `SetMaxOpenConns(1)` on the write handle (fed only by the eventual batching writer from Task 8), a normal multi-connection pool for reads. Generate slugs with `crypto/rand` as 12-character base62. Store operation payloads as JSON. Wrap each appended batch in one transaction.

- [x] **Step 2: Add persistence and recovery tests**

Use `t.TempDir()` and assert: slugs are exactly 12 characters, base62, case-sensitive unique; `PRAGMA journal_mode` returns `wal`; recovery after snapshot + post-snapshot operation replay reproduces the same CRDT text (per `gopad-schema.md` §5 room startup/recovery flow).

- [x] **Step 3: Verify persistence**

Run: `go test -race ./internal/store -v`
Expected: PASS, including restart recovery.

The regular persistence suite and full `go test ./...` plus `go vet ./...` pass locally. The GitHub Actions CGO workflow passed the race-enabled store suite.

- [x] **Step 4: Commit**

```bash
git add internal/store
git commit -m "feat(store): persist documents operations and snapshots"
```

### Task 4: Document HTTP API and static routes

**Files:**
- Modify: `internal/server/server.go`
- Create: `internal/server/documents.go`
- Create: `web/index.html`
- Create: `web/editor.html`
- Create: `web/assets/style.css`
- Test: `internal/server/documents_test.go`

**Interfaces:**
- Consumes: `Store.CreateDocument(context.Context)`
- Produces: `POST /documents`, `GET /`, `GET /d/{slug}`

- [x] **Step 1: Implement handlers and pages**

Embed `web/` with `//go:embed`. `POST /documents` decodes no request body, calls `CreateDocument`, returns `201` with `application/json` body `{"slug":"..."}` via `json.NewEncoder`. `GET /` serves the landing/creation page; its button posts to `/documents` and redirects to `/d/{slug}`. `GET /d/{slug}` validates the slug against `^[0-9A-Za-z]{12}$` before any store lookup and returns `404` for malformed or unknown slugs.

- [x] **Step 2: Add HTTP behavior tests**

Cover: `POST /documents` returns `201`/JSON/valid slug; `GET /` serves the creation page; valid document paths serve the editor; malformed or missing slugs return `404`.

- [x] **Step 3: Verify HTTP behavior**

Run: `go test ./internal/server -v`
Expected: PASS.

- [x] **Step 4: Commit**

```bash
git add internal/server web
git commit -m "feat(http): add document creation and static editor routes"
```

### Task 5: Room lifecycle, sequencing, and WebSocket transport

**Files:**
- Create: `internal/realtime/protocol.go`
- Create: `internal/realtime/hub.go`
- Create: `internal/realtime/room.go`
- Create: `internal/realtime/client.go`
- Test: `internal/realtime/room_test.go`
- Test: `internal/realtime/websocket_test.go`
- Modify: `internal/server/server.go`

**Interfaces:**
- Consumes: CRDT and store interfaces from Tasks 2–3
- Produces: `NewHub(store Store) *Hub`, `(*Hub).GetOrCreateRoom(context.Context, string) (*Room, error)`, `GET /ws/{slug}`

- [x] **Step 1: Implement the room and protocol**

Define `Envelope{Type string, Payload json.RawMessage}` and typed `op`, `cursor`, `presence`, and `sync` payloads. Give each room one owner goroutine that only does in-memory state mutation (register/unregister/apply-op), a bounded per-client send queue, and a shared bounded fan-out worker pool per `gopad-architecture.md` §9.1 — **the event loop submits fan-out jobs and does not wait for them to complete** (see Global Constraints). Assign a monotonic per-document server sequence to each op distinct from CRDT `(counter, siteID)` order. Room eviction: a cancellable idle timer, defaulting to 45 seconds after the last client disconnects (configurable via `GOPAD_ROOM_IDLE_TIMEOUT`, injectable in tests), removing the room from the hub's `sync.Map` when it fires with zero clients still connected.

- [x] **Step 2: Implement the WebSocket transport**

`GET /ws/{slug}` upgrades via `coder/websocket`, resolves the room through `Hub.GetOrCreateRoom`, and registers the connection. Enforce a 16 KiB read limit, ping/pong keepalive with read-deadline reset on pong, a write deadline on the writePump, and proper close frames on shutdown or error.

- [x] **Step 3: Add room and transport tests**

With fake clients and a fake store: assert monotonic per-document sequence assignment, sender exclusion from its own broadcast, FIFO delivery per client, slow-client removal without stalling others, one room instance per slug (`GetOrCreateRoom` idempotence), and eviction firing at the configured timeout under a test-injected clock — with zero clients required at fire time, not just at trigger time.

With `coder/websocket` test clients against `httptest.Server`: open two connections, assert the second receives an initial `sync`, then receives a subsequent `op` broadcast from the first.

- [x] **Step 4: Verify transport and races**

Run: `go test -race ./internal/realtime ./internal/server -v`
Expected: PASS.

The focused and full regular suites plus `go vet ./...` pass locally. The
GitHub Actions CGO workflow passed the race-enabled transport and server suites.

- [x] **Step 5: Commit**

```bash
git add go.mod go.sum internal/realtime internal/server/server.go
git commit -m "feat(realtime): add sequenced collaborative rooms"
```

### Task 6: Browser CRDT, editing, and reconnect synchronization

**Files:**
- Create: `web/assets/crdt.js`
- Create: `web/assets/ws.js`
- Create: `web/assets/app.js`
- Create: `web/assets/crdt.test.js`
- Modify: `web/editor.html`
- Modify: `web/assets/style.css`

**Interfaces:**
- Produces: `RgaDocument.apply(op)`, `RgaDocument.text()`, `SocketClient.connect()`, DOM-to-operation translation

- [x] **Step 1: Implement the browser replica**

Use ES modules and the same comparator and tombstone semantics as the Go CRDT (Task 2), applied to a plain `textarea` (per `gopad-spec.md` §4 — simplest, avoids `contenteditable` cursor/selection quirks). Persist a random username prompt result in `localStorage`; accept the server-issued site ID on connect. Diff textarea input into insert/delete operations against the local visible-view mapping, apply locally first (optimistic), then send.

- [x] **Step 2: Implement reconnect and resync**

Reconnect with capped exponential backoff: 250ms, 500ms, 1s, 2s, 4s, 8s. On every `sync` message, replace local CRDT state wholesale with the authoritative snapshot rather than attempting to merge — matches the v1 resync decision (re-fetch snapshot, don't replay unacked local ops). Disable editing while disconnected so no keystrokes are silently lost.

- [x] **Step 3: Add a zero-build JavaScript test runner**

Create `crdt.test.js` using `node:test` and `node:assert/strict`. Mirror the Go convergence cases from Task 2 and add visible-offset-to-`leftID` mapping tests specific to the browser diffing logic.

- [x] **Step 4: Verify browser logic**

Run: `node --test web/assets/crdt.test.js && go test ./...`
Expected: PASS.

The six zero-build browser CRDT tests, full Go suite, and `go vet ./...` pass
locally.

- [x] **Step 5: Commit**

```bash
git add web
git commit -m "feat(editor): add optimistic CRDT editing and reconnect"
```

### Task 7: Presence and cursor overlays

**Files:**
- Create: `web/assets/presence.js`
- Create: `web/assets/presence.test.js`
- Modify: `web/assets/app.js`
- Modify: `web/editor.html`
- Modify: `web/assets/style.css`
- Modify: `internal/realtime/protocol.go`
- Modify: `internal/realtime/room.go`
- Test: `internal/realtime/room_test.go`

**Interfaces:**
- Consumes: cursor messages expressed with adjacent CRDT `CharID` values
- Produces: join/leave/name/color broadcasts, collaborator count, remote caret labels

- [ ] **Step 1: Implement ephemeral presence**

Find-or-create the user by username (per `gopad-schema.md` §1/§4) and assign a stable stored color at creation time, reused across sessions. Broadcast active-user snapshots on join/leave; never persist `cursor` or `presence` messages (they skip the DB entirely, per `gopad-architecture.md` §3.3). Throttle browser cursor sends to 50ms. Render remote carets in an overlay aligned to a textarea mirror element, anchored to CRDT `CharID`s rather than raw offsets so they stay correct as the document changes underneath them.

- [ ] **Step 2: Add presence tests**

Server-side: joins and leaves update the active list without touching the store; cursor messages from the same user coalesce (latest wins) rather than queuing. Browser-side: cursor anchors remain attached to the correct character after a concurrent insert shifts surrounding text.

- [ ] **Step 3: Verify presence**

Run: `go test -race ./internal/realtime -v && node --test web/assets/*.test.js`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/realtime web
git commit -m "feat(presence): show collaborators and remote cursors"
```

### Task 8: Write batching, snapshots, shutdown, and observability

**Files:**
- Create: `internal/store/writer.go`
- Test: `internal/store/writer_test.go`
- Create: `internal/metrics/metrics.go`
- Modify: `internal/realtime/room.go`
- Modify: `internal/server/server.go`
- Modify: `cmd/gopad/main.go`
- Create: `loadtest/websocket.js`
- Create: `README.md`

**Interfaces:**
- Produces: `Writer.EnqueueOps`, `Writer.EnqueueSnapshot`, `Writer.Flush`, `/metrics`, private `/debug/pprof/`, graceful shutdown

- [ ] **Step 1: Implement background persistence**

One dedicated writer goroutine fed by a bounded job channel (per `gopad-architecture.md` §9.3 — do not let multiple goroutines call the write handle directly). Op-batch flush triggers at 100 operations or 250ms, whichever first. Snapshot triggers at 1,000 operations or 30 seconds, whichever first, coalescing multiple pending signals into one in-flight snapshot (buffered-size-1 trigger channel, per §9.2). The snapshot path requests a deep copy of CRDT state from the room's owner goroutine (cheap struct copy) and does the expensive JSON marshal + DB write off that copy, outside the room event loop. Both thresholds resolve the two tuning questions left open in `gopad-architecture.md` §10 — update that doc's open-questions section to reflect these are now fixed, not still open.

- [ ] **Step 2: Wire room eviction and shutdown to the writer**

Confirm the room idle-eviction default from Task 5 (`GOPAD_ROOM_IDLE_TIMEOUT`, 45s) is documented alongside the new batching env vars. On graceful shutdown: stop accepting new connections, request a final flush from every active room's writer queue, close existing WebSocket connections with a proper close frame, then exit — do not exit while flushes are still in flight.

- [ ] **Step 3: Add batching and shutdown tests**

With a fake executor and fake clock: assert flush fires at 100 ops or 250ms; snapshot fires at 1,000 ops or 30s; multiple rapid snapshot triggers coalesce into one in-flight write; a full/backed-up queue surfaces as a metric rather than blocking the room loop; shutdown waits for the final flush to complete before returning.

- [ ] **Step 4: Verify batching and shutdown**

Run: `go test -race ./internal/store ./internal/realtime ./internal/server -v`
Expected: PASS.

- [ ] **Step 5: Add metrics and profiling**

Record active connections/rooms (gauges), operations processed (counter), broadcast/fan-out latency (histogram), write-batch size/latency (histogram), write-queue depth (gauge), and dropped/slow-client count (counter) with Prometheus collectors. Mount `/metrics` on the main listener. Mount `/debug/pprof/*` only when `GOPAD_ENABLE_PPROF=1`, and bind it to an internal-only listener, never the public one — per `AGENTS.md` security guidance.

- [ ] **Step 6: Add the load scenario and operating guide**

`loadtest/websocket.js` (k6): create documents, connect a configurable number of virtual users, emit timestamped edits at human-like intervals, and check round-trip receipt latency against a threshold. Document `GOPAD_DB_PATH`, `GOPAD_ADDR`, `GOPAD_ROOM_IDLE_TIMEOUT`, `GOPAD_ENABLE_PPROF`, load-test thresholds, development commands, and the username-only trust model (per `gopad-schema.md` §1 — usernames are claims, not authenticated identities) in `README.md`.

- [ ] **Step 7: Run the final quality gate**

Run: `gofmt -w cmd internal && go test -race ./... && go vet ./... && node --test web/assets/*.test.js`
Expected: every command passes with no race reports.

Run locally: `go run ./cmd/gopad`, then `k6 run loadtest/websocket.js`.
Expected: health and metrics endpoints respond, collaborative edits converge, and the configured latency thresholds pass.

- [ ] **Step 8: Commit**

```bash
git add cmd internal loadtest README.md
git commit -m "feat(ops): add durable batching metrics and load testing"
```
