# Contributing

The project is intentionally small and explicit.

Guidelines:

- keep provider-specific logic behind `internal/provider/<provider>`
- keep storage abstractions in `internal/store`
- do not couple application logic directly to SQLite types
- add end-to-end tests for sync or publish behavior changes

Before sending changes:

```sh
go test ./...
go run ./cmd/serial-sync --config ./examples/config.demo.toml plan sync
go run ./cmd/serial-sync --config ./examples/config.demo.toml sync
go run ./cmd/serial-sync --config ./examples/config.demo.toml publish
```
