# Developer Guide

This repo is a standalone Go sync utility with a generic core and a Patreon-first MVP.

## Code Shape

- `internal/app` owns orchestration.
- `internal/provider` defines provider seams.
- `internal/store` defines repository seams.
- `internal/store/sqlite` is the current persistence backend.
- `internal/artifact` and `internal/publish` handle artifact materialization and replayable publishing.

## Working Locally

```sh
go test ./...
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check
go run ./cmd/serial-sync --config ./examples/config.demo.toml run --dry-run
go run ./cmd/serial-sync --config ./examples/config.demo.toml run
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup auth
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup dump --auth-profile patreon-default --path ./serial-sync-rule-workspace --force
```

## Generated Assets

```sh
$(go env GOPATH)/bin/sqlc generate
$(go env GOPATH)/bin/cue vet experimental/cue/config.cue examples/config.demo.toml -d '#Config'
```

## Conventions

- Keep provider-specific logic inside `internal/provider/<provider>`.
- Keep SQLite-specific logic inside `internal/store/sqlite`.
- Avoid leaking provider response shapes into the app or store layers.
- Add an end-to-end test when behavior changes across sync or publish flows.

## Current Scope

- Patreon supports both live auth and the fixture demo flow.
- Patreon source dumping can enumerate paid memberships into a local rule-authoring workspace.
- `filesystem` and `exec` publishing are implemented.
- the public CLI is organized around `setup`, `run`, and `debug`.
- `run`, `setup auth`, and the single-process `run daemon` are implemented.
- session-bundle import, richer challenge handling, and richer daemon coordination remain future work.

## Docs

- [Config reference](docs/config.md)
- [Architecture](docs/architecture.md)
- [Control plane notes](docs/control-plane.md)
- [Observability guide](docs/observability.md)
- [PRD status](docs/prd-status.md)
- [Provider notes](docs/patreon.md)
- [Provider contribution guide](docs/provider-contributing.md)
- [Hook tutorial](docs/hooks.md)
