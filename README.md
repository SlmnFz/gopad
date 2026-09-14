# Gopad

[![CI](https://github.com/SlmnFz/gopad/actions/workflows/ci.yml/badge.svg)](https://github.com/SlmnFz/gopad/actions/workflows/ci.yml)

Gopad is a self-hostable, open-source collaborative plain-text editor. It is designed to feel familiar like a lightweight Google Docs, while keeping the realtime collaboration machinery visible and understandable: CRDTs, WebSockets, presence, persistence, and room concurrency.

Gopad is intentionally small. The server is written in Go, the browser client uses vanilla HTML/CSS/JavaScript, and the data layer uses SQLite. There is no frontend build system and no opaque collaboration service.

## What it provides

- Shareable document links with no account setup.
- Concurrent plain-text editing with an RGA-style sequence CRDT.
- Optimistic local edits and deterministic convergence across replicas.
- Tombstone deletes that preserve character identity for concurrent operations.
- WebSocket rooms with reconnect synchronization and live presence.
- SQLite snapshots and operation history for restart recovery.
- A single-node architecture with clear seams for future scaling.
- Metrics, profiling controls, and load-test documentation as the system matures.

Rich text, accounts, permissions, offline editing, undo/redo, and abuse protection are deliberately outside the v1 scope.

## Architecture

```text
browser replicas
      │ WebSocket envelopes
      ▼
Go HTTP server ── active document rooms ── canonical CRDT state
      │
      ▼
SQLite snapshots + operation history
```

Each inserted character has a permanent `(siteID, counter)` identity. Concurrent siblings use a deterministic `(counter, siteID)` ordering, so replicas converge regardless of delivery order. Deletes set tombstones instead of removing characters.

## Documentation

- [Project specification](docs/gopad-spec.md)
- [Server architecture](docs/gopad-architecture.md)
- [Database schema](docs/gopad-schema.md)
- [v1 implementation plan](docs/gopad-v1-implementation-plan.md)
- [Documentation index](docs/README.md)
- [Contributing guide](CONTRIBUTING.md)

## Quick start

Requirements: Go 1.22 or newer.

```powershell
git clone https://github.com/SlmnFz/gopad.git
cd gopad
go run ./cmd/gopad
```

The development server listens on `http://localhost:8080`. Verify it is running:

```powershell
Invoke-WebRequest -UseBasicParsing http://localhost:8080/healthz
```

The implementation is delivered in small, verifiable milestones. The plan documents the complete v1 target and the current status of each task.

## Development

Run the full local quality checks:

```powershell
gofmt -w cmd internal
go test ./...
go test -race ./...
go vet ./...
```

Keep Go commands under `cmd/gopad`, server packages under `internal/`, browser files under `web/`, migrations under `internal/store/migrations/`, and load tests under `loadtest/`.

Every push to `main` and every pull request runs the same formatting, race-enabled test, and vet checks in GitHub Actions on Ubuntu with `CGO_ENABLED=1`.

## v1 trust model

Document slugs are the access mechanism in v1. Anyone who has a document link can view and edit it. Usernames are display identities, not authenticated accounts. Do not use an unmodified v1 deployment for sensitive documents or untrusted public traffic.

## Contributing

Bug reports, design discussions, tests, documentation improvements, and focused implementation changes are welcome. Start with the [contributing guide](CONTRIBUTING.md), then read the relevant design document before changing protocol, schema, or concurrency behavior.

## License

Gopad is released under the [MIT License](LICENSE).
