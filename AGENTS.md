# Repository Guidelines

## Project Structure & Module Organization

Gopad is a design-first repository. Root documents define the intended implementation:

- `gopad-spec.md`: user flows, WebSocket protocol, CRDT behavior, and milestones.
- `gopad-architecture.md`: Go server, room concurrency, persistence, scaling, and observability.
- `gopad-schema.md`: SQLite tables, connection handshake, snapshots, and operation replay.
- `gopad-v1-implementation-plan.md`: the approved, task-level implementation plan.

When implementation begins, keep Go commands under `cmd/gopad`, server packages under `internal/`, browser files under `web/`, migrations under `internal/store/migrations/`, and load tests under `loadtest/`. Keep CRDT, persistence, realtime rooms, and HTTP delivery as separate packages.

## Build Order

Implementation follows `gopad-v1-implementation-plan.md` task by task, in final-shape order: server foundation → CRDT → SQLite store → document HTTP API → realtime rooms/WebSocket transport → browser CRDT client → presence → write batching/observability. There is no separate throwaway "dumb broadcast, no CRDT" milestone — the transport and concurrency plumbing (room lifecycle, sequencing, WebSocket handling) is validated with fakes and integration tests in its own task before the browser CRDT client lands, so the naive-first approach isn't needed to de-risk networking separately from CRDT correctness.

Each task in the plan is implemented, then verified (run it, confirm behavior), then covered with tests, in that order — not test-first. See the plan's own header for the exact workflow.

Do not add sharded rooms across multiple processes, a pub/sub bus, or Postgres migration while v1 is in progress — those are explicit stretch goals in `gopad-architecture.md` §5.3 and §4.2, not part of this build.

## Build, Test, and Development Commands

There is no executable code or build system yet. Once the Go module exists, use:

- `go run ./cmd/gopad`: run the server locally.
- `go test ./...`: execute all unit and integration tests.
- `go test -race ./...`: detect concurrency errors in rooms and client handling.
- `go vet ./...`: catch suspicious Go constructs.
- `gofmt -w .`: format Go sources before review.

Record any new frontend or load-testing commands here when their tooling is added.

## Coding Style & Naming Conventions

Use tabs and `gofmt` for Go; use two spaces in JavaScript, JSON, SQL, and Markdown examples. Go packages and files use short lowercase names; exported identifiers use `PascalCase`, internal identifiers use `camelCase`, and tests use `TestBehavior_Context`. Keep WebSocket message types lowercase (`op`, `cursor`, `presence`, `sync`) and preserve schema names such as `site_id` and `snapshot_version`. Distinguish CRDT ordering from server-assigned operation sequence numbers.

## Testing Guidelines

Use Go's `testing` package with table-driven tests. Place unit tests beside their source as `*_test.go`; put SQLite or WebSocket integration tests in the owning package. Every concurrency change should pass `go test -race ./...`. Each task in the implementation plan adds tests for the behavior it introduces — implement first, verify it runs, then add tests, per the plan's stated workflow. Cumulative coverage across the full plan includes convergence under reordered concurrent operations, snapshot replay, room eviction, backpressure, and reconnect synchronization; a given task only needs to cover what it actually adds.

## Commit & Pull Request Guidelines

This directory has no Git history, so no existing convention can be inferred. Use focused Conventional Commits, for example `feat(crdt): merge concurrent inserts`. Pull requests should explain the behavior, cite the relevant spec section, list verification commands, and call out schema or protocol changes. Include screenshots for editor or presence UI changes and benchmark results for performance claims.

## Security & Configuration

Do not commit databases, secrets, or local environment files. The v1 username-only identity model is intentionally unauthenticated; document any change that expands its trust boundary. Keep `/debug/pprof` private and enforce WebSocket size limits, deadlines, and slug validation.
