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
go run ./cmd/serial-sync --config ./examples/config.demo.toml plan sync
go run ./cmd/serial-sync --config ./examples/config.demo.toml run once
go run ./cmd/serial-sync --config ./examples/config.demo.toml auth bootstrap
go run ./cmd/serial-sync --config ./examples/config.demo.toml source discover --auth-profile patreon-default
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
- Patreon discovery can suggest sources and starter rules from active memberships.
- `filesystem` and `exec` publishing are implemented.
- `run once`, `auth bootstrap`, and the single-process `daemon` are implemented.
- session-bundle import, richer challenge handling, and richer daemon coordination remain future work.

## Docs

- [Config reference](docs/config.md)
- [Architecture](docs/architecture.md)
- [Control plane notes](docs/control-plane.md)
- [Source discovery guide](docs/source-discovery.md)
- [Observability guide](docs/observability.md)
- [PRD status](docs/prd-status.md)
- [Provider notes](docs/patreon.md)
- [Provider contribution guide](docs/provider-contributing.md)
- [Hook tutorial](docs/hooks.md)
