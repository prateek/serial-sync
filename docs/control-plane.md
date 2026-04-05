# Control Plane Notes

These notes capture the current direction for persistence, CLI structure, and config schema work.

## Current Direction

- Use `sqlc` for the SQLite layer now.
- Keep the hand-rolled CLI for the moment; if we replace it, prefer `kong`.
- Treat CUE as an optional validation and config-shaping layer, not the runtime source of truth.

## Why `sqlc`

- The repo already has a stable SQLite schema and a repository interface.
- `sqlc` removes handwritten query scanning while keeping `database/sql` and the current store boundary.
- The migration cost is small and the maintenance win is immediate.

## Why `kong` If We Replace The CLI

- The existing command tree is nested enough that struct-shaped commands map cleanly.
- `kong` reduces parser boilerplate without pushing the repo into Cobra-style scaffolding.
- `urfave/cli` remains a reasonable alternative, but it fits better when we want command handlers centered around framework callbacks and richer built-in CLI packaging features.

## Why Not CUE-First Yet

- The runtime config surface is still small and has only one real Go consumer.
- Go structs in `internal/config` are still the most straightforward runtime model.
- Adding CUE as the primary source of truth right now would introduce a second modeling layer before we have enough reuse to justify it.

## Incremental CUE Path

- Keep `internal/config/config.go` as the runtime shape.
- Use `experimental/cue/config.cue` to validate TOML and express stronger enum and relationship constraints.
- Revisit CUE-first generation only if config needs to be shared across multiple consumers or emitted in multiple formats from one schema.

## Useful Commands

```sh
$(go env GOPATH)/bin/sqlc generate
$(go env GOPATH)/bin/cue vet experimental/cue/config.cue examples/config.demo.toml -d '#Config'
$(go env GOPATH)/bin/cue export experimental/cue/config.cue examples/config.demo.toml -d '#Config' --out toml
```
