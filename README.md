# Gopad

Gopad is a small Google-Docs-style collaborative plain-text editor built to make realtime collaboration concepts tangible. It uses a Go server, WebSockets, SQLite, and a hand-written RGA-style sequence CRDT.

The project is intentionally educational and is being built in small, verifiable increments. Task 1 currently provides the Go HTTP server foundation and `/healthz`; collaborative editing features are still under construction.

## Documentation

- [Project specification](docs/gopad-spec.md)
- [Server architecture](docs/gopad-architecture.md)
- [Database schema](docs/gopad-schema.md)
- [v1 implementation plan](docs/gopad-v1-implementation-plan.md)
- [Contributing guide](CONTRIBUTING.md)

## Development

Requirements: Go 1.22 or newer.

```powershell
go run ./cmd/gopad
go test ./...
go test -race ./...
go vet ./...
gofmt -w cmd internal
```

Once the server is running, the health endpoint is available at `http://localhost:8080/healthz`.

## v1 scope

Gopad v1 is plain text, link-shared, and intentionally has no authentication or permissions. Anyone with a document link can edit it. Rich text, accounts, offline editing, undo/redo, and abuse protection are out of scope for v1.

## License

Gopad is licensed under the [MIT License](LICENSE).
