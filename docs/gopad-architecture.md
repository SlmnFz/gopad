# Gopad — Server Architecture & Scaling

Covers the hub/room structure, HTTP/WebSocket handling, persistence write path, horizontal scaling strategy, observability, and load testing approach. This is the design target for making Gopad a "real app" that can be meaningfully load tested — not just a two-tab demo.

## 1. Design targets

- **v1 scale target**: single-node, but architected so horizontal scaling is a config/routing change, not a rewrite.
- Many documents (rooms) existing, most idle at any given time; a smaller number actively being edited concurrently by a handful of users each.
- Full observability from day one, since a load test without metrics just tells you "it fell over," not why.

## 2. Single-node capacity notes

Goroutines are cheap (~2-4KB stack, grows on demand) — the "2 goroutines per connection + 1 per active room" pattern scales to hundreds of thousands of goroutines without goroutines themselves being the bottleneck. The real single-node constraints:

- **File descriptors**: each WebSocket connection is a socket. Raise OS limits (`ulimit -n`, and the container/systemd equivalent) if targeting 10k+ concurrent connections.
- **Memory per room**: each `Room` holds an in-memory canonical CRDT (full char list + tombstones) + client registry. Small per-room for text docs, but measure at scale with many thousands of simultaneously-live rooms rather than assuming.
- **Broadcast fan-out cost**: a room with N clients means every op triggers N sends. Fine at doc-editing scale (5-10 concurrent editors per doc). A "many viewers on one doc" pattern (more like a webinar) would need a different broadcast strategy (dedicated fan-out pool, or batched-flush-per-tick instead of per-keystroke) — not a v1 concern, but worth naming as a different problem shape.

## 3. Hub → sharded room registry

```go
type Hub struct {
    rooms sync.Map // map[string(slug)]*Room — concurrent-safe, optimized for heavy-read/rare-write
    db    *sql.DB
}

func (h *Hub) GetOrCreateRoom(ctx context.Context, slug string) *Room {
    if r, ok := h.rooms.Load(slug); ok {
        return r.(*Room)
    }
    r := newRoom(slug, h.db)
    actual, loaded := h.rooms.LoadOrStore(slug, r)
    if !loaded {
        go r.Run(ctx) // room's event loop starts only once, on first client
    }
    return actual.(*Room)
}
```

Properties:
- **Lazy room activation** — a room costs no goroutine/memory until someone connects. Matters when most documents are idle at any moment.
- **`sync.Map`** over `map` + `sync.RWMutex` — matches the actual access pattern (frequent lookups on connect, rare writes on room creation).
- **Room eviction** — rooms must eventually free themselves or the server leaks memory as documents accumulate over its lifetime.

```go
// inside Room.Run's event loop
case <-idleTimer.C:
    if len(r.clients) == 0 {
        h.rooms.Delete(r.slug)
        return // goroutine exits, room is gone until next GetOrCreateRoom
    }
```

Room eviction uses a grace period after the last client disconnects (not immediate teardown), so a quick reconnect doesn't force a full reload-from-DB. Exact grace-period duration is an open tuning question — start with something like 30-60s and adjust based on observed reconnect behavior.

## 4. Persistence write path

SQLite has a **single-writer** model — even in WAL mode (`PRAGMA journal_mode=WAL`, which should always be on), only one write transaction commits at a time across the *entire database*, not per-table or per-row. If every keystroke from every room writes to `operations` immediately, all writes across *all rooms* serialize through one lock — this becomes the real bottleneck under concurrent load, well before WebSocket throughput or goroutine count do.

### 4.1 Write-behind batching (not write-through)

Decouple persistence from the broadcast hot path:

```
op arrives → apply to in-memory CRDT → broadcast immediately (latency-critical, this is what users feel as typing lag)
           → append to an in-memory write buffer for that room
```

A separate ticker per room (e.g. every 200-500ms, or every N ops, whichever comes first) flushes the buffer as one batched `INSERT` transaction. Broadcast latency is now fully decoupled from DB write latency. Durability window: worst case, the last ~200-500ms of unflushed ops are lost on a crash — an explicit, acceptable v1 tradeoff.

### 4.2 Known ceiling, stated upgrade path

SQLite in WAL mode with batched writes handles a meaningful amount of concurrent load for a learning-scale project. The schema (see `gopad-schema.md`) is written in boring standard SQL and is portable to Postgres without redesign. Plan: build and load-test against SQLite first; if the load test shows write-lock contention as the actual bottleneck (not something else), that's the trigger to swap to Postgres — measure before swapping, don't guess upfront.

## 5. Horizontal scaling

### 5.1 The core problem

If multiple server instances run behind a load balancer, and two collaborators on the same document connect to different instances, each instance's in-memory `Room` has no way to know about the other instance's ops — the in-memory-canonical-CRDT design assumes one process owns each room.

### 5.2 Option A — Sticky routing by document (v1 approach)

Route all connections for a given `slug` to the same backend instance, always — e.g. consistent hashing on the slug at the load-balancer/routing layer. Each `Room` still only ever exists on one instance; none of the Hub/Room code changes, a routing layer is simply added in front. Simpler to build and operate, and a broadly useful pattern beyond this project.

### 5.3 Option B — Shared pub/sub bus (stretch goal)

Any instance can host any room. When a room applies an op, it also publishes to a `doc:{slug}` channel (NATS or Redis pub/sub); instances subscribe to channels for rooms they're currently serving clients for, relaying bus messages into local room broadcast. More flexible (no sticky routing, better rebalancing), but adds a new service, a new failure mode, and cross-instance message ordering to reason about.

**Decision**: build for Option A now (sticky routing), treat Option B as a deliberate later milestone once true horizontal scaling is the explicit goal.

## 6. HTTP + WebSocket layer

```
net/http + chi (or stdlib pattern routing, Go 1.22+)
  POST   /documents          → create doc, return slug
  GET    /                   → serve landing/create page (static HTML)
  GET    /d/{slug}           → serve editor page (static HTML)
  GET    /ws/{slug}          → WebSocket upgrade, enters hub.GetOrCreateRoom flow
  GET    /healthz            → liveness/readiness for load balancer
  GET    /metrics            → Prometheus scrape endpoint
  GET    /debug/pprof/*      → profiling, gate behind internal-only network/auth
```

Connection-level details that matter most under load:

- **Ping/pong keepalive** — supported by `coder/websocket`. Without it, dead connections (phone locked, wifi dropped) sit in the `clients` map forever, consuming memory and causing failed broadcast attempts. Set a read deadline, reset on pong, close + clean up on timeout.
- **Max message size** — cap it (a single op should never be large) so a buggy or malicious client can't send an oversized payload and blow up memory.
- **Per-client backpressure** — already designed into the broadcast loop (drop slow clients rather than blocking the room). Add a **write deadline** on the writePump so a half-dead TCP connection can't hang a goroutine indefinitely.
- **Graceful shutdown** — on SIGTERM: stop accepting new connections, flush all rooms' pending write buffers to DB, close existing connections with a proper close frame, then exit. Important for development (frequent restarts) and for not silently losing data on deploys.

## 7. Observability

- **Prometheus metrics**: active connections (gauge), active rooms (gauge), ops/sec (counter), broadcast latency (histogram — time from op received to last client's send enqueued), DB write batch size/latency, goroutine count.
- **pprof**: Go's built-in profiler — essential for diagnosing "why is this slow / leaking memory" during a load test. Network-restricted, never exposed publicly.
- **Structured logging**: connection open/close, room create/evict, errors — `log/slog` (stdlib since Go 1.21).

## 8. Load testing approach

- **k6 + `xk6-websockets`** — standard tool, scriptable, good reporting, low effort to get started.
- **Hand-rolled Go load client** — spin up N goroutines, each opening a WS connection, simulating realistic "typing" patterns (random inserts at human-like intervals) against a shared pool of document slugs; measure round-trip broadcast latency via a timestamp embedded in each op, compared on receipt. Extra benefit: writing this exercises the same readPump/writePump plumbing from the client side, reinforcing the same concepts from the other direction.

## 9. Concurrency design — fan-out, snapshotting, SQLite writes

The room's core event-loop goroutine must only ever do fast, in-memory state mutation. Every slow operation — fan-out send, snapshot marshal, DB write — is handed off to a separate goroutine/pool connected via channels, so no single slow client, slow disk, or slow marshal can stall the loop that's supposed to be handling the next incoming op. This section covers the three places that principle gets applied.

### 9.1 Room fan-out — bounded worker pool, no ordering guarantee needed

The naive broadcast (`for c := range r.clients { c.send <- data }`) run inline inside the event loop is a problem at scale: that same loop also processes `register`/`unregister`/incoming ops, so any non-trivial fan-out time blocks the *next* op from being processed — op throughput for a hot room degrades as its client count grows, even with non-blocking per-client sends.

**Fix**: parallelize fan-out, keep the event loop to state mutation only.

```go
case msg := <-r.broadcast:
    clients := r.snapshotClients() // cheap slice copy, done inline (safe — single writer)
    for _, c := range clients {
        if c == msg.from {
            continue
        }
        fanoutJobs <- FanoutJob{client: c, data: msg.data}
    }
```

Fan-out is submitted to a **fixed worker pool** (per-room or shared across rooms) rather than spawning a goroutine per client per broadcast — at sustained load (e.g. 50 ops/sec × 20 clients = 1000 sends/sec), ad-hoc `go func()` per send adds real goroutine-churn overhead. A bounded pool caps concurrency intentionally instead of letting it spike with client count.

```go
type FanoutJob struct {
    client *Client
    data   []byte
}

func fanoutWorker(jobs <-chan FanoutJob) {
    for job := range jobs {
        select {
        case job.client.send <- job.data:
        case <-time.After(sendTimeout):
            // slow client — signal for cleanup/unregister
        }
    }
}
```

**Ordering decision**: whether to wait for all fan-out sends to complete before the event loop proceeds to the next op is a real tradeoff — waiting guarantees strict delivery order but bounds room throughput by the slowest client; not waiting is higher throughput but loses cross-client delivery-order guarantees. Gopad **does not wait**: CRDT correctness doesn't depend on delivery order (that's the point of the CRDT — designed for out-of-order/concurrent delivery), so per-client ordering is left entirely to each client's own buffered, FIFO `send` channel. This is a case where CRDT correctness directly simplifies the concurrency design.

### 9.2 Snapshot triggering — debounced background goroutine, deep-copy handoff

Snapshotting (serializing the full CRDT state, including tombstones, to `documents.snapshot`) is comparatively expensive (full walk + JSON marshal) and must never run on the event-loop goroutine.

**Design**: a dedicated snapshot goroutine per active room, triggered by a debounced signal channel plus a time-based fallback.

```go
type Room struct {
    // ...
    snapshotTrigger  chan struct{} // buffered size 1 — coalesces signals, no pileup
    opsSinceSnapshot int
}

// inside the event loop, after applying an op:
r.opsSinceSnapshot++
if r.opsSinceSnapshot >= snapshotOpThreshold {
    select {
    case r.snapshotTrigger <- struct{}{}:
    default: // snapshot already pending/in-flight — don't queue more
    }
    r.opsSinceSnapshot = 0
}
```

```go
func (r *Room) snapshotWorker(ctx context.Context) {
    ticker := time.NewTicker(maxSnapshotInterval) // time-based fallback trigger
    for {
        select {
        case <-r.snapshotTrigger:
        case <-ticker.C:
        case <-ctx.Done():
            return
        }
        state := r.requestStateCopy() // safe cross-goroutine read, see below
        persistSnapshot(ctx, dbWriteQueue, r.slug, state) // slow I/O off the hot path
    }
}
```

**Safe state read across goroutines**: the snapshot goroutine needs CRDT state owned by the event-loop goroutine, without a data race and without blocking the loop for a full marshal. v1 approach: the snapshot goroutine requests a **deep copy** of the char list over a channel; the event loop responds with the copy (cheap relative to marshaling — just struct copying) and immediately continues; the snapshot goroutine does the expensive JSON marshal + DB write on its own time, off the copy. (A more advanced approach — structuring CRDT storage as append-only/copy-on-write so a "snapshot" needs no copy at all — is how some production CRDT implementations work internally, but is a bigger lift; noted as a v2 idea, not built now.)

### 9.3 SQLite writes — single dedicated writer, separate read path

Two write streams now hit the DB: batched operation inserts (write-behind flush, see §4.1) and snapshot writes (§9.2). Both need to funnel through SQLite's single-writer constraint in a controlled way, rather than letting every room's flush/snapshot goroutine call `db.Exec` directly and relying on SQLite's own locking to sort out the contention.

**Design**: a single dedicated DB-writer goroutine (or small bounded pool), fed by a channel — an explicit write-serialization queue.

```go
type WriteJob struct {
    kind    string // "ops_batch" | "snapshot"
    slug    string
    payload any
    done    chan error // optional — caller can wait for confirmation
}

func dbWriter(ctx context.Context, writeDB *sql.DB, jobs <-chan WriteJob) {
    for job := range jobs {
        var err error
        switch job.kind {
        case "ops_batch":
            err = writeOpsBatch(writeDB, job.slug, job.payload.([]Op))
        case "snapshot":
            err = writeSnapshot(writeDB, job.slug, job.payload.(SnapshotData))
        }
        if job.done != nil {
            job.done <- err
        }
    }
}
```

Why serialize writes explicitly instead of trusting `database/sql`'s connection pool + SQLite's own locking:

- **Intentional batching/coalescing** — multiple rooms' pending op-batches landing close together in time can be coalesced into fewer, larger transactions, meaningfully cheaper than many small transactions (fewer fsync/WAL-checkpoint round trips).
- **Visible backpressure** — a bounded channel filling up is a clean, metric-able signal that writes are falling behind, versus goroutines invisibly blocked inside the connection pool waiting on SQLite's lock.
- **The default connection pool doesn't help here and can actively hurt** — concurrent Go-level writers just serialize behind SQLite's lock anyway, and multiple connections fighting over that lock adds contention/retry overhead versus one connection with an internal queue.

**Two separate `*sql.DB` handles**, not one pool serving both purposes:
- **Write handle**: `db.SetMaxOpenConns(1)` — single connection, fed only by the `dbWriter` goroutine.
- **Read handle**: normal multi-connection pool, used for late-joiner catch-up (replaying `operations` after a snapshot) and any other reads — SQLite in WAL mode allows concurrent readers alongside the single writer, so this path doesn't contend with the write path.

This split is easy to miss and directly determines whether "concurrent editors across many rooms" actually scales, or silently bottlenecks on connection-pool contention layered on top of SQLite's own lock.

### 9.4 Summary

| Concern | Pattern |
|---|---|
| Room fan-out | Bounded worker pool (per-room or shared), non-blocking, no wait-for-completion — relies on CRDT order-independence |
| Snapshot trigger | Dedicated per-room goroutine, debounced signal channel + time-based fallback, deep-copy handoff from event loop |
| SQLite writes | Single dedicated writer goroutine (or tiny bounded pool) fed by a channel; separate single-conn write handle vs. multi-conn read handle |

## 10. Open tuning questions

- Exact room idle-eviction grace period (proposed starting point: 30-60s).
- Write-buffer flush interval / batch size threshold (proposed starting point: 200-500ms or N ops, whichever first).
- Snapshot trigger policy — still open from `gopad-schema.md` (every N ops? every X seconds? on last-client-disconnect?). Related to, but distinct from, the write-buffer flush — snapshotting rewrites `documents.snapshot`, flushing writes to `operations`.
