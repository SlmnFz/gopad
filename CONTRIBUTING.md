# Contributing to Gopad

Thanks for helping build Gopad. The repository is intentionally small and design-first, so keep changes focused and aligned with the documents in [`docs/`](docs/).

## Before submitting a change

- Read the relevant specification and implementation-plan task.
- Keep Go code under `cmd/` and `internal/`, browser code under `web/`, migrations under `internal/store/migrations/`, and load tests under `loadtest/`.
- Run `gofmt -w cmd internal` for Go changes.
- Run `go test ./...` and `go vet ./...`; use `go test -race ./...` for concurrency-sensitive changes.
- Update documentation when behavior, configuration, schema, or protocol changes.

## Commit messages

Use focused Conventional Commits, such as:

```text
feat(crdt): merge concurrent inserts
fix(realtime): remove stalled clients
docs: clarify snapshot recovery
```

Pull requests should explain the behavior changed, cite the relevant design document, list verification commands, and call out protocol or schema changes. Include screenshots for browser UI changes and benchmark results for performance claims.

## Scope

The v1 trust model is deliberately username-only and unauthenticated. Do not add authentication, permissions, multi-process room sharding, a pub/sub bus, or a Postgres migration without first updating the design documents and plan.
