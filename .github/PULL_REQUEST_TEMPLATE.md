## What changed?

<!-- Explain the behavior or documentation change in a few sentences. -->

## Why?

<!-- Link the relevant task, specification, or issue. -->

## Verification

- [ ] `gofmt -w cmd internal`
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `node --test web/assets/*.test.js` (when browser files changed)
- [ ] `go test -race ./...` (when concurrency-sensitive code changed)

## Checklist

- [ ] Documentation and configuration changes are included.
- [ ] No secrets, databases, or local environment files are committed.
- [ ] I have called out schema, protocol, or operational changes below.

## Notes for reviewers

<!-- Include screenshots, benchmark results, migration notes, or follow-up work. -->
