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

- [x] **Step 1: Implement ephemeral presence**

Find-or-create the user by username (per `gopad-schema.md` §1/§4) and assign a stable stored color at creation time, reused across sessions. Broadcast active-user snapshots on join/leave; never persist `cursor` or `presence` messages (they skip the DB entirely, per `gopad-architecture.md` §3.3). Throttle browser cursor sends to 50ms. Render remote carets in an overlay aligned to a textarea mirror element, anchored to CRDT `CharID`s rather than raw offsets so they stay correct as the document changes underneath them.

- [x] **Step 2: Add presence tests**

Server-side: joins and leaves update the active list without touching the store; cursor messages from the same user coalesce (latest wins) rather than queuing. Browser-side: cursor anchors remain attached to the correct character after a concurrent insert shifts surrounding text.

- [x] **Step 3: Verify presence**

Run: `go test -race ./internal/realtime -v && node --test web/assets/*.test.js`
Expected: PASS.

The eight browser CRDT/presence tests, full Go suite, and `go vet ./...` pass
locally. The local race run is blocked by the missing Windows C compiler for
`CGO_ENABLED=1`; GitHub Actions remains the race-verification path.

- [x] **Step 4: Commit**

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

- [x] **Step 1: Implement background persistence**

One dedicated writer goroutine fed by a bounded job channel (per `gopad-architecture.md` §9.3 — do not let multiple goroutines call the write handle directly). Op-batch flush triggers at 100 operations or 250ms, whichever first. Snapshot triggers at 1,000 operations or 30 seconds, whichever first, coalescing multiple pending signals into one in-flight snapshot (buffered-size-1 trigger channel, per §9.2). The snapshot path requests a deep copy of CRDT state from the room's owner goroutine (cheap struct copy) and does the expensive JSON marshal + DB write off that copy, outside the room event loop. Both thresholds resolve the two tuning questions left open in `gopad-architecture.md` §10 — update that doc's open-questions section to reflect these are now fixed, not still open.

- [x] **Step 2: Wire room eviction and shutdown to the writer**

Confirm the room idle-eviction default from Task 5 (`GOPAD_ROOM_IDLE_TIMEOUT`, 45s) is documented alongside the new batching env vars. On graceful shutdown: stop accepting new connections, request a final flush from every active room's writer queue, close existing WebSocket connections with a proper close frame, then exit — do not exit while flushes are still in flight.

- [x] **Step 3: Add batching and shutdown tests**

With a fake executor and fake clock: assert flush fires at 100 ops or 250ms; snapshot fires at 1,000 ops or 30s; multiple rapid snapshot triggers coalesce into one in-flight write; a full/backed-up queue surfaces as a metric rather than blocking the room loop; shutdown waits for the final flush to complete before returning.

- [x] **Step 4: Verify batching and shutdown**

Run: `go test -race ./internal/store ./internal/realtime ./internal/server -v`
Expected: PASS.

Focused non-race tests pass locally, and the race-enabled verification passed in GitHub Actions with CGO enabled.

- [x] **Step 5: Add metrics and profiling**

Record active connections/rooms (gauges), operations processed (counter), broadcast/fan-out latency (histogram), write-batch size/latency (histogram), write-queue depth (gauge), and dropped/slow-client count (counter) with Prometheus collectors. Mount `/metrics` on the main listener. Mount `/debug/pprof/*` only when `GOPAD_ENABLE_PPROF=1`, and bind it to an internal-only listener, never the public one — per `AGENTS.md` security guidance.

- [x] **Step 6: Add the load scenario and operating guide**

`loadtest/websocket.js` (k6): create documents, connect a configurable number of virtual users, emit timestamped edits at human-like intervals, and check round-trip receipt latency against a threshold. Document `GOPAD_DB_PATH`, `GOPAD_ADDR`, `GOPAD_ROOM_IDLE_TIMEOUT`, `GOPAD_ENABLE_PPROF`, load-test thresholds, development commands, and the username-only trust model (per `gopad-schema.md` §1 — usernames are claims, not authenticated identities) in `README.md`.

- [x] **Step 7: Run the final quality gate**

Run: `gofmt -w cmd internal && go test -race ./... && go vet ./... && node --test web/assets/*.test.js`
Expected: every command passes with no race reports.

Local Go, vet, and browser checks pass; GitHub Actions also passed the race-enabled Go quality gate with CGO enabled.

Run locally: `go run ./cmd/gopad`, then `k6 run loadtest/websocket.js`.
Expected: health and metrics endpoints respond, collaborative edits converge, and the configured latency thresholds pass.

- [x] **Step 8: Commit**

```bash
git add cmd internal loadtest README.md
git commit -m "feat(ops): add durable batching metrics and load testing"
```

### Task 9: In-process TTL cache for hot document lookups
 
**Files:**
- Create: `internal/cache/cache.go`
- Test: `internal/cache/cache_test.go`
- Modify: `internal/server/documents.go`
- Modify: `internal/realtime/hub.go`
- Modify: `cmd/gopad/main.go`
**Interfaces:**
- Consumes: `store.Store` read paths (`LoadDocument`, slug-existence checks)
- Produces: `cache.New[K, V](ttl time.Duration, maxEntries int) *Cache[K, V]`, `(*Cache[K, V]).Get(key K) (V, bool)`, `(*Cache[K, V]).Set(key K, value V)`, `(*Cache[K, V]).Delete(key K)`, `(*Cache[K, V]).Len() int`
- [x] **Step 1: Implement a generic in-memory TTL cache**
A single `internal/cache` package, no external deps. Backing store is a `map[K]entry[V]` guarded by a `sync.RWMutex`, `entry` holds `value V` and `expiresAt time.Time`. `Get` checks expiry lazily on read (an expired-but-not-yet-swept entry is treated as a miss) rather than relying solely on a sweeper. A background goroutine sweeps expired entries every `ttl/2` (bounded, started in `New`, stopped via a `Close()` the caller defers) so a cold cache doesn't grow unbounded from misses that were never re-read. Enforce `maxEntries` with simple oldest-expiry eviction on `Set` — this is a size backstop, not an LRU, so keep the eviction scan O(n) only on the rare overflow path, not the hot path.
 
- [x] **Step 2: Wire the cache into the hot lookup paths**
Cache two things, each with its own instance and TTL so document-not-found results don't stick around as long as found ones: (a) `GET /d/{slug}` and `GET /ws/{slug}`'s "does this slug exist" check (short TTL, e.g. 5s — this is the per-request path hit on every page load and every reconnect), and (b) nothing from the write path — inserted/edited document *content* is never cached, since the room's in-memory CRDT is already the source of truth for anything live and the cache would just add a staleness bug. On `CreateDocument` success, do not pre-populate the cache; let the next read populate it, keeping the cache dumb (read-through, not write-through).
 
- [x] **Step 3: Add cache tests**
Cover: `Get` on a missing key returns `(zero, false)`; entries expire after their TTL and are treated as misses even before the sweeper runs; `Set` past `maxEntries` evicts something rather than growing unbounded; concurrent `Get`/`Set` under `-race` don't corrupt state; `Close()` stops the sweeper goroutine (assert via a goroutine-count check or a closed-channel signal, not a sleep-and-hope).
 
- [ ] **Step 4: Verify**
Run: `go test -race ./internal/cache ./internal/server ./internal/realtime -v`
Expected: PASS, including the concurrent cache test under `-race`.

Local non-race verification passes; the Windows host has no GCC for `-race`, so this verification is pending GitHub Actions.
 
- [x] **Step 5: Commit**
```bash
git add internal/cache internal/server internal/realtime cmd
git commit -m "feat(cache): add TTL cache for slug-existence lookups"
```
 
### Task 10: Terminal-style editor theme and Persian (RTL) support
 
**Files:**
- Modify: `web/assets/style.css`
- Modify: `web/editor.html`
- Modify: `web/index.html`
- Modify: `web/assets/app.js`
- Create: `web/assets/avatar.js`
- Create: `web/assets/fonts/` (self-hosted Vazirmatn woff2 subset for Persian; keep a Latin monospace stack as the default)
- Test: `web/assets/app.test.js`
- Test: `web/assets/avatar.test.js`
**Interfaces:**
- Consumes: existing `RgaDocument`/`SocketClient` from Task 6, presence overlay and stable per-user color from Task 7
- Produces: theme CSS custom properties, a per-user direction toggle, `detectDirection(text)` helper, `renderAvatar(username, size) -> SVGElement`
- [x] **Step 1: Build the terminal-noir theme**
Restyle around CSS custom properties on `:root` — near-black background, phosphor-green primary text (`#33ff33`-ish, tuned for contrast against the Global Constraints' plain-text focus, not neon-glow-heavy), monospace stack (`ui-monospace, "SF Mono", "Cascadia Code", monospace`) for the editor and chrome alike. Subtle scanline/vignette treatment via a low-opacity repeating-linear-gradient overlay — cheap CSS, no canvas/JS, and disabled under `prefers-reduced-motion`/`prefers-contrast: more` for accessibility. Collaborator cursor colors (Task 7) must stay legible against the dark background — clamp assigned colors to a minimum lightness rather than using raw hash-derived hues. No frontend build system per Global Constraints — plain CSS custom properties, no preprocessor.
 
- [x] **Step 2: Add deterministic per-user SVG avatars**
`avatar.js` exports `renderAvatar(username, size)`, pure and client-only — no server round trip, no new store table. Hash the username with a small FNV-1a implementation to get a 32-bit seed, feed it through a seeded PRNG (mulberry32 or similar, inlined — no new dependency), and derive a mirrored 5×5 geometric grid plus a background/foreground hue pair from the seed, matching the reference demo built earlier in this conversation. Reuse the same seed source as the stable per-user color from Task 7's presence system (derive both from the username hash) so a given collaborator's avatar and cursor color always agree. Render the avatar next to usernames in the collaborator list and next to remote cursor labels; keep it to the terminal theme's palette (clamp hue/lightness the same way cursor colors are clamped in Step 1) so avatars don't clash with the dark background.
 
- [x] **Step 3: Add Persian/RTL handling**
Self-host a Vazirmatn (or similar open-license Persian-friendly) webfont subset covering Persian + Latin so no external font CDN is required (keeps the "serve static assets directly" constraint intact). Add a `detectDirection(text)` helper in `app.js` using the Unicode bidi character ranges to guess RTL vs LTR from the first strongly-directional character typed, and apply `dir="auto"` plus a manual override toggle in the editor chrome (some users will paste mixed content and want to pin direction rather than have it flip under them). Confirm the CRDT layer is untouched by this — direction and font are purely presentational; character IDs, `LeftID` ordering, and the textarea diffing from Task 6 operate on the logical character sequence regardless of visual RTL/LTR rendering, so no changes to `crdt.js` are needed here.
 
- [x] **Step 4: Add tests for direction logic and avatars**
`app.test.js` (same zero-build `node:test` runner as Task 6): `detectDirection` returns `rtl` for Persian-first strings, `ltr` for Latin-first strings, and a stable default for direction-neutral input (digits/punctuation only). Assert the manual override, once set, is not overwritten by subsequent auto-detection on the same session. `avatar.test.js`: `renderAvatar` is deterministic (same username twice produces identical markup), different usernames produce different output with overwhelming probability (spot-check a sample list for collisions), and output stays a valid, small SVG (bounded node count, no external references).
 
- [x] **Step 5: Verify**
Run: `node --test web/assets/*.test.js && go test ./internal/server -v`
Expected: PASS; manually confirm in-browser that typing Persian text renders RTL with correct glyph shaping, that avatars are stable across reconnects, and that the theme respects `prefers-reduced-motion`.

Verification: browser preview confirmed the connected editor, deterministic collaborator avatar, Persian RTL auto-detection, manual direction override, and AUTO reset. Local Node tests, `go test ./...`, and `go vet ./...` pass.

- [x] **Step 6: Commit**
```bash
git add web
git commit -m "feat(ui): add terminal theme, avatars, and Persian RTL support"
```
 
### Task 11: CRDT convergence fuzzing, simulated network faults, and scale benchmarks
 
**Files:**
- Create: `internal/crdt/harness_test.go`
- Create: `internal/crdt/property_test.go`
- Create: `internal/crdt/fuzz_test.go`
- Create: `internal/crdt/bench_test.go`
- Create: `internal/crdt/testdata/fuzz/` (seed corpus, populated by Step 4)
- Modify: `internal/crdt/document.go` (only if a small exported hook is needed for deep-copy/inspection in tests — prefer test-only helpers first)
**Interfaces:**
- Consumes: `Document`, `Operation`, `CharID` from Task 2 — no production behavior change, this task is test/bench-only unless Step 1 finds a real gap
- Produces: `simNetwork` (in-package test helper: N replicas, injectable delivery order/drop/duplicate/partition), a random-but-valid `Operation` generator, `go test -fuzz=FuzzTwoReplicaConverge`, `go test -bench=.` suite
- [x] **Step 1: Build a multi-replica simulation harness**
`harness_test.go` defines `simNetwork`, owning N in-memory `*Document` replicas plus a queue of pending `(fromReplica, op)` deliveries. Support: arbitrary reordering of the pending queue, dropping a message (with optional later redelivery to simulate retry), duplicating a message (must be a no-op given CRDT idempotence — Task 2 already covers single-duplicate; this harness now covers duplicates arriving interleaved with unrelated ops), and partitioning a subset of replicas (their ops queue locally and are withheld from the rest) followed by healing (queued ops flush across the partition boundary in random order). Every harness run takes an explicit `*rand.Rand` seeded from a single `int64`, and on any assertion failure the test must print that seed plus the full delivered-op sequence in replay order — the whole point of this harness is that a failure is reproducible by rerunning with the printed seed, not just "it failed once."
 
- [x] **Step 2: Add a random-but-valid operation generator**
Generate `Insert`/`Delete` sequences that are individually well-formed against Task 2's rules (a `Delete` always targets a `CharID` some replica has actually seen inserted; an `Insert`'s `LeftID` is either the zero value or a previously-generated ID) but whose *arrival order across replicas* is deliberately adversarial — that's what exercises convergence, not malformed input (malformed input belongs to Task 2's typed-error tests, already covered). Bias the generator toward concurrent siblings (multiple replicas inserting after the same `LeftID` in the same round) and toward delete/insert races (one replica deletes a character while another concurrently inserts after it), since those are RGA's actual hard cases.
 
- [x] **Step 3: Add the property-based convergence test**
`property_test.go`: `TestProperty_NReplicasConverge` runs many iterations (start at 500, tunable via `-short`/env var so CI can trim it) varying replica count (2–5), op count per run (10–200), and fault mix (plain reorder / drop+redeliver / duplicate / partition+heal), using the Step 1 harness and Step 2 generator. After every op has been delivered to every replica (including any withheld-then-healed ones), assert all replicas' `Text()` are identical. This is the test that actually earns the "I stress-tested my CRDT" claim — the existing Task 2 tests are worked examples, this is the search.
 
- [x] **Step 4: Add native Go fuzzing on top of the same invariant**
`fuzz_test.go`: `FuzzTwoReplicaConverge(f *testing.F)` decodes fuzzer-provided bytes into a bounded op sequence (reuse Step 2's construction logic, just driven by fuzz bytes instead of `math/rand`) and asserts two-replica convergence under both original and reversed delivery order. Seed the corpus (`testdata/fuzz/`) with a handful of hand-picked regression cases: empty doc, single insert, delete-then-insert-at-same-spot, and any case Step 3 finds and you choose to pin permanently. `go test -fuzz` runs are opt-in/time-boxed locally and in a separate CI job, not part of the default `go test ./...` gate, since unbounded fuzzing time doesn't belong in every PR run.
 
- [x] **Step 5: Add scale benchmarks that surface the tombstone-growth cost**
`bench_test.go`: `BenchmarkApply` and `BenchmarkText` at 1k/10k/100k/1M total ops, each run at a few delete ratios (0%, 30%, 70%) since deletes-as-tombstones (Global Constraints — tombstones are never removed) is the known RGA cost center; the point is to make the curve visible, not to hit a target number. Run with `-benchmem` to also track allocations per op. Record the current numbers in a short `internal/crdt/BENCHMARKS.md` so future changes have something to diff against.
 
- [ ] **Step 6: Verify**
Run: `go test -race ./internal/crdt -run Property -v` (property suite), `go test -race ./internal/crdt -v` (existing Task 2 suite still green), `go test ./internal/crdt -fuzz=FuzzTwoReplicaConverge -fuzztime=60s` (local fuzz smoke run — not part of the standard gate), `go test ./internal/crdt -bench=. -benchmem -run=^$`.
Expected: property and regular suites PASS under `-race`; the 60s fuzz run finds no crashes; benchmarks complete and show the expected superlinear cost as tombstone count grows (confirms the harness is measuring something real, not a no-op).

Verification so far: the 500-iteration property suite, all regular CRDT tests, the 60-second fuzz run (112,102 executions), and the full benchmark matrix pass without `-race`. The local Windows host has no GCC, so the two `-race` commands are pending the CGO-enabled GitHub Actions job added in this task.

- [x] **Step 7: Commit**
```bash
git add internal/crdt
git commit -m "test(crdt): add property-based convergence fuzzing and scale benchmarks"
```

### Task 12: Dockerize gopad and add a Prometheus/Grafana stack
 
**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`
- Create: `docker-compose.yml`
- Create: `deploy/prometheus/prometheus.yml`
- Create: `deploy/grafana/provisioning/datasources/prometheus.yml`
- Create: `deploy/grafana/provisioning/dashboards/dashboards.yml`
- Create: `deploy/grafana/dashboards/gopad.json`
- Modify: `internal/metrics/metrics.go` (only if metric names need aligning with this task's dashboard — see Step 1)
- Modify: `README.md`
**Interfaces:**
- Consumes: `/metrics` and `/healthz` from Task 8, `GOPAD_*` env vars from Tasks 8–9
- Produces: a `gopad:local` Docker image, `docker compose up` bringing up gopad + Prometheus + Grafana, a provisioned Grafana dashboard
- [ ] **Step 1: Fix the canonical metric names**
Before writing any dashboard JSON, pin down the exact Prometheus metric names Task 8 registered (gauges/counter/histograms for active connections, active rooms, operations processed, broadcast latency, write-batch size/latency, write-queue depth, dropped/slow-client count). If Task 8's implementation used ad hoc names, rename them now to a consistent `gopad_` prefix (e.g. `gopad_active_connections`, `gopad_active_rooms`, `gopad_operations_total`, `gopad_broadcast_latency_seconds`, `gopad_write_batch_size`, `gopad_write_batch_latency_seconds`, `gopad_write_queue_depth`, `gopad_dropped_clients_total`) so the dashboard in Step 5 has a stable contract to build against. Write the final list into `internal/metrics/metrics.go`'s doc comment as the source of truth.
 
- [ ] **Step 2: Write the Dockerfile**
Multi-stage build: a `golang:1.22` builder stage running `CGO_ENABLED=0 go build` (safe since `modernc.org/sqlite` is pure Go per the Tech Stack — no cgo toolchain needed in the image), producing a static binary with `web/` already baked in via the existing `//go:embed` (Task 4) so the final image needs nothing but the binary. Final stage is `gcr.io/distroless/static` (or `scratch` plus a copied CA-cert bundle if any TLS-consuming code needs it later), running as a non-root numeric UID, `EXPOSE 8080`, `ENTRYPOINT ["/gopad"]`. Declare a `VOLUME` at the directory `GOPAD_DB_PATH` defaults to, so the SQLite file survives container recreation. Add `.dockerignore` excluding `.git`, `loadtest/`, `*.md`, and any local `*.db` files to keep the build context small.
 
- [ ] **Step 3: Write docker-compose with gopad, Prometheus, and Grafana**
Three services on one dedicated bridge network: `gopad` (built from the Dockerfile, `GOPAD_ADDR=:8080` published to the host, a named volume for the SQLite path, `GOPAD_ENABLE_PPROF` left unset/`0` by default since Task 8's constraint is that pprof must never be on the public listener — do not publish a pprof port from compose); `prometheus` (official `prom/prometheus` image, mounts `deploy/prometheus/prometheus.yml` read-only, no published port needed beyond what's used for local debugging); `grafana` (official `grafana/grafana` image, mounts the `deploy/grafana/provisioning/` tree read-only so the datasource and dashboard load automatically with zero manual clicking, publishes `3000` to the host, default admin credentials documented in README as dev-only).
 
- [ ] **Step 4: Configure the Prometheus scrape target**
`deploy/prometheus/prometheus.yml`: one scrape job named `gopad`, target `gopad:8080`, path `/metrics`, a scrape interval matched to the metric resolution that's actually useful here (15s is plenty given the histogram buckets from Task 8, no need for sub-second scraping on a hobby project).
 
- [ ] **Step 5: Build the Grafana dashboard**
`deploy/grafana/dashboards/gopad.json`: panels for active connections and active rooms (gauges/time series), operations processed (rate, counter), broadcast/fan-out latency (histogram heatmap or p50/p95/p99 time series via `histogram_quantile`), write-batch size and latency (same treatment), write-queue depth (gauge, since Task 8 explicitly wants a full/backed-up queue visible as a metric rather than silently blocking), and dropped/slow-client count (rate). Reference only the metric names fixed in Step 1. Provisioning YAML (`datasources/prometheus.yml`, `dashboards/dashboards.yml`) points Grafana at the `prometheus` service by its compose network name and auto-loads this dashboard on startup — no manual "add data source" step for anyone cloning the repo.
 
- [ ] **Step 6: Verify the stack end to end**
Run: `docker compose up --build`, then `curl localhost:8080/healthz`, `curl localhost:8080/metrics` (confirm the Step 1 metric names actually appear), open Prometheus at `localhost:9090/targets` and confirm the `gopad` job is `UP`, open Grafana at `localhost:3000` and confirm the dashboard is present and wired to real data. Generate traffic with the existing `loadtest/websocket.js` (Task 8) against the containerized instance and confirm the dashboard panels move.
Expected: all four checks pass with no manual configuration beyond `docker compose up`.
 
- [ ] **Step 7: Document it**
Add a "Running with Docker" section to `README.md`: `docker compose up --build`, the three URLs (app, Prometheus, Grafana), where the SQLite volume lives, and an explicit note that the bundled Grafana admin credentials are for local development only and must be changed before any non-local deployment.
 
- [ ] **Step 8: Commit**
```bash
git add Dockerfile .dockerignore docker-compose.yml deploy internal/metrics README.md
git commit -m "feat(ops): dockerize gopad and add Prometheus/Grafana stack"
```
