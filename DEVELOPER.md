# Developer Guide

This repo is a standalone Go sync utility with a generic core and a Patreon-first MVP.

## Code Shape

- `internal/app` owns orchestration.
- `internal/provider` defines provider seams.
- `internal/store` defines repository seams.
- `internal/store/sqlite` is the current persistence backend.
- `internal/config` compiles the authored rules, inputs, review rules and overrides into one read-only rule set behind `Config.Compiled`.
- `internal/classify`, `internal/discovery` and `internal/sequence` decide and detect sequence numbers; `internal/app/decisions.go` is the one release decider.
- `internal/artifact` handles canonical artifact materialization.
- `internal/publish` hosts the filesystem and exec target adapters behind one `Target` seam; `internal/app/transitions.go` plans each target's delivery once.

## Working Locally

```sh
go test ./...
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup check
go run ./cmd/serial-sync --config ./examples/config.demo.toml run --dry-run
go run ./cmd/serial-sync --config ./examples/config.demo.toml run
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup auth
go run ./cmd/serial-sync --config ./examples/config.demo.toml setup dump --auth-profile patreon-default --path ./serial-sync-rule-workspace
```

The `internal/artifact` tests shell out to `epubcheck` and Calibre's `ebook-convert`, so both must be on `PATH`. On macOS, `brew install epubcheck` and install Calibre; on Linux, `scripts/install-epubcheck` installs the same pinned EPUBCheck release the Docker image and CI use.

CI runs the suite in a `golang:1.27-trixie` container so it tests against the Calibre the Docker image ships (Debian trixie's 8.5.0). Ubuntu 24.04's packaged Calibre 7.6.0 crashes on EPUB 3 output, so the PDF conversion test fails there; see [Troubleshooting](docs/troubleshooting.md#pdf-to-epub-conversion-fails-on-ubuntu-2404).

CI uses `go test -timeout 20m ./...` because the app's EPUB workflows exceed Go's default ten-minute package timeout on hosted runners. Each EPUBCheck invocation still has its own two-minute timeout.

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
- Patreon source dumping can enumerate paid memberships into a local series-authoring workspace.
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
