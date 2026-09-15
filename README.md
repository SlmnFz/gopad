<div align="center">

<img src="web/assets/gopad-logo.svg" alt="Gopad logo" width="112">

# Gopad

**A self-hostable collaborative plain-text editor with the machinery in view.**

[![CI](https://github.com/SlmnFz/gopad/actions/workflows/ci.yml/badge.svg)](https://github.com/SlmnFz/gopad/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-4dd8c0.svg)](LICENSE)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-4dd8c0.svg)](go.mod)

Share a link, open the document, and edit together in real time. Gopad keeps
the collaboration layer deliberately small and understandable: Go, vanilla
HTML/CSS/JavaScript, WebSockets, an RGA-style CRDT, and SQLite.

[Try the demo](https://slmnfz.github.io/gopad/) · [Read the docs](docs/README.md) · [Contribute](CONTRIBUTING.md)

</div>

## Why Gopad?

Gopad is a design-first open-source editor for people who want shared plain
text without handing the document model to an opaque hosted service. It is
small enough to run on a laptop, clear enough to study, and structured so the
realtime, persistence, and browser layers can evolve independently.

### Features

- Link-shared documents with no account setup.
- Concurrent plain-text editing with deterministic CRDT convergence.
- WebSocket rooms with reconnect synchronization and live presence.
- SQLite snapshots and operation history for restart recovery.
- Persian-friendly RTL direction detection and manual direction control.
- Terminal-noir UI with deterministic per-user SVG avatars.
- Prometheus metrics plus a provisioned Grafana dashboard.
- k6 WebSocket load testing and Go property/fuzz tests for the CRDT.

Rich text, authentication, permissions, offline editing, undo/redo, and abuse
protection are intentionally outside the v1 trust boundary. See [SECURITY.md](SECURITY.md)
before exposing an instance to untrusted traffic.

## Architecture

~~~text
browser replicas
      │ WebSocket envelopes
      ▼
Go HTTP server ── active document rooms ── canonical CRDT state
      │                         │
      ▼                         ▼
SQLite snapshots + history   Prometheus metrics
                                      │
                                      ▼
                                  Grafana
~~~

Each inserted character has a permanent (siteID, counter) identity.
Concurrent siblings use deterministic (counter, siteID) ordering, so replicas
converge regardless of delivery order. Deletes create tombstones instead of
removing character identity.

## Quick start

Requirements: Go 1.22 or newer.

~~~powershell
git clone https://github.com/SlmnFz/gopad.git
cd gopad
go run ./cmd/gopad
~~~

Open <http://localhost:8080>, create a document, and share its /d/<slug> link.
The development server stores its SQLite database in gopad.db by default.

Verify the server is healthy:

~~~powershell
Invoke-WebRequest -UseBasicParsing http://localhost:8080/healthz
~~~

## Running with Docker

The local observability stack starts Gopad, Prometheus, and Grafana with the
configuration already wired together:

~~~powershell
Copy-Item .env.example .env
docker compose up --build
~~~

Edit `.env` first if the default ports are already in use. The example covers
the published app, Prometheus, and Grafana ports, all supported Gopad runtime
settings, and the local Grafana login. `.env` is ignored by Git; never commit
real credentials.

- App: <http://localhost:8080>
- Prometheus: <http://localhost:9090>
- Grafana: <http://localhost:3000>

The SQLite database is stored in the named gopad-data volume at
/data/gopad.db. The bundled Grafana development login is admin / admin.
These credentials are for local development only; change them before any
non-local deployment. Do not publish the Prometheus or Grafana ports without
adding the access controls appropriate for your environment.

## Configuration

All settings are optional:

| Variable | Default | Purpose |
| --- | --- | --- |
| GOPAD_DB_PATH | gopad.db | SQLite database path |
| GOPAD_ADDR | :8080 | Public HTTP and WebSocket listener |
| GOPAD_LOG_LEVEL | info | Structured log level: debug, info, warn, or error |
| GOPAD_ROOM_IDLE_TIMEOUT | 45s | Grace period before an idle room is evicted |
| GOPAD_WRITE_QUEUE_SIZE | 1024 | Maximum queued persistence jobs |
| GOPAD_OP_BATCH_SIZE | 100 | Operations that trigger a write flush |
| GOPAD_OP_FLUSH_INTERVAL | 250ms | Maximum operation write delay |
| GOPAD_SNAPSHOT_OP_THRESHOLD | 1000 | Operations that trigger a snapshot copy |
| GOPAD_SNAPSHOT_INTERVAL | 30s | Maximum snapshot interval for dirty rooms |
| GOPAD_ENABLE_PPROF | unset | Enables pprof on loopback-only 127.0.0.1:6060 |

Docker Compose also accepts `GOPAD_HTTP_PORT`, `PROMETHEUS_PORT`, and
`GRAFANA_PORT` for host-side port mappings, plus `GRAFANA_ADMIN_USER`,
`GRAFANA_ADMIN_PASSWORD`, and `GRAFANA_ALLOW_SIGN_UP` for the local Grafana
instance. See [.env.example](.env.example) for a complete starting point.

GET /healthz is the lightweight readiness check. GET /metrics exposes the
canonical Prometheus metrics for connections, rooms, operations, fan-out
latency, write batches, queue depth, and dropped clients. pprof is never
mounted on the public listener.

## Development

Run the standard local checks:

~~~powershell
gofmt -w cmd internal
go test ./...
go vet ./...
node --test web/assets/*.test.js
~~~

For concurrency-sensitive changes, also run:

~~~powershell
go test -race ./...
~~~

The CRDT property suite, native fuzz target, and scale benchmarks are kept
close to the implementation:

~~~powershell
go test ./internal/crdt -run Property -v
go test ./internal/crdt -fuzz=FuzzTwoReplicaConverge -fuzztime=60s
go test ./internal/crdt -bench=. -benchmem -run=^$
~~~

See [internal/crdt/BENCHMARKS.md](internal/crdt/BENCHMARKS.md) for the current
baseline. Every push to main and pull request runs formatting, race-enabled
Go tests, vet, browser tests, and a bounded CRDT fuzz job in GitHub Actions.

### Load testing

Install [k6](https://grafana.com/docs/k6/latest/), start Gopad, and run the
shared-document WebSocket scenario:

~~~powershell
k6 run loadtest/websocket.js
~~~

Override the target and load with GOPAD_BASE_URL, GOPAD_VUS, GOPAD_DURATION,
and GOPAD_SOCKET_SECONDS. The scenario checks that at least 95% of echoed edits
arrive within the configured 500ms round-trip threshold.

## Documentation

- [Project specification](docs/gopad-spec.md) — product scope, flows, protocol, and CRDT behavior.
- [Server architecture](docs/gopad-architecture.md) — rooms, persistence, scaling, and observability.
- [Database schema](docs/gopad-schema.md) — SQLite tables, handshake, snapshots, and recovery.
- [v1 implementation plan](docs/gopad-v1-implementation-plan.md) — ordered tasks and verification.
- [Documentation index](docs/README.md)

## Open-source project

Contributions are welcome, including bug reports, design discussions, tests,
documentation, and focused implementation changes.

- Read [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.
- Follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- Report vulnerabilities privately using [SECURITY.md](SECURITY.md).
- Use the issue templates for [bugs](.github/ISSUE_TEMPLATE/bug_report.yml) and
  [feature requests](.github/ISSUE_TEMPLATE/feature_request.yml).

Please keep changes aligned with the specification and implementation plan.
Avoid adding authentication, multi-process room sharding, a pub/sub bus, or a
Postgres migration to v1 without first updating the design documents.

## License

Gopad is released under the [MIT License](LICENSE).
