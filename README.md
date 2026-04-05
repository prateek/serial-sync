# serial-sync

`serial-sync` is a standalone Go utility for syncing serialized reading content from authenticated sources into durable artifacts and replayable publishers.

This repo implements the first real MVP from the PRD:

- provider-agnostic core types and store/publisher seams
- Patreon-shaped provider contract with realistic fixture-backed normalization
- declarative single-file config with XDG defaults
- one-shot `plan sync`, `sync`, `publish`, and inspect commands
- SQLite-backed run, event, release, track, artifact, and publish state
- deterministic filesystem artifact storage and filesystem publishing
- support-bundle export

The current implementation is intentionally honest about scope:

- live Patreon auth and live discovery are not wired yet
- the Patreon provider currently reads raw Patreon API fixture payloads from `fixture_dir`
- `filesystem` publishing is implemented; `exec`, `wizard`, and `daemon` are reserved seams

That keeps the repo runnable immediately while preserving the architecture the PRD calls for.

## Quickstart

1. Initialize a config:

   ```sh
   go run ./cmd/serial-sync init --path ./examples/config.demo.toml --force
   ```

2. Replace the file with the demo config in [examples/config.demo.toml](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/examples/config.demo.toml).

3. Validate the config:

   ```sh
   go run ./cmd/serial-sync --config ./examples/config.demo.toml config validate
   ```

4. Plan a sync:

   ```sh
   go run ./cmd/serial-sync --config ./examples/config.demo.toml plan sync
   ```

5. Materialize artifacts:

   ```sh
   go run ./cmd/serial-sync --config ./examples/config.demo.toml sync
   ```

6. Publish them:

   ```sh
   go run ./cmd/serial-sync --config ./examples/config.demo.toml publish
   ```

7. Inspect state:

   ```sh
   go run ./cmd/serial-sync --config ./examples/config.demo.toml source inspect plum-parrot
   go run ./cmd/serial-sync --config ./examples/config.demo.toml run inspect <run-id>
   ```

## Repo layout

- `cmd/serial-sync`: CLI entrypoint.
- `internal/app`: orchestration shared by `plan`, `sync`, `publish`, and inspect commands.
- `internal/provider`: provider seam.
- `internal/provider/patreon`: Patreon fixture-backed normalization.
- `internal/store/sqlite`: SQLite state implementation.
- `internal/artifact`: canonical artifact selection and storage.
- `internal/publish`: replayable publisher implementations.
- `testdata/fixtures`: realistic Patreon-style sample inputs.
- `docs/`: operator and contributor docs.

## Current behavior

`sync` performs:

- source enumeration
- release normalization
- story-track assignment using declarative rules
- canonical artifact selection
- durable release, track, artifact, run, and event persistence

`publish` performs:

- filesystem export from stored canonical artifacts
- idempotent publish tracking keyed by artifact hash and target

Dry-run support exists for both flows:

```sh
go run ./cmd/serial-sync --config ./examples/config.demo.toml sync --dry-run
go run ./cmd/serial-sync --config ./examples/config.demo.toml publish --dry-run
```

## Docs

- [Config Reference](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/config.md)
- [Docker Quickstart](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/docker-quickstart.md)
- [Troubleshooting](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/troubleshooting.md)
- [Architecture](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/architecture.md)
- [Provider Notes](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/patreon.md)
- [Provider Contribution Guide](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/provider-contributing.md)
- [Hook Tutorial](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/docs/hooks.md)
- [Product PRD](/Users/prateek/code/experiments/2026-04-03-calibre-setup/serial-sync/serial-sync-prd.md)

## License

Apache-2.0.
