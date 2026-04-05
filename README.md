# serial-sync

`serial-sync` syncs serialized content from authenticated sources into durable artifacts and replayable publishers.

## Quickstart

```sh
go run ./cmd/serial-sync init --path ./examples/config.demo.toml --force
go run ./cmd/serial-sync --config ./examples/config.demo.toml sync
go run ./cmd/serial-sync --config ./examples/config.demo.toml publish
```

Use `--dry-run` first if you want to preview changes:

```sh
go run ./cmd/serial-sync --config ./examples/config.demo.toml sync --dry-run
go run ./cmd/serial-sync --config ./examples/config.demo.toml publish --dry-run
```

## What It Does

- syncs releases from provider-backed sources
- classifies them into story tracks
- materializes canonical artifacts on disk
- publishes those artifacts to filesystem or exec-hook targets
- records runs, events, and publish history in SQLite

## Current Scope

- Patreon is the first provider
- the MVP currently uses fixture-backed Patreon payloads
- `filesystem` and `exec` publishing are implemented
- live auth, `wizard`, and `daemon` are future work

## More

- [Developer guide](DEVELOPER.md)
- [Config reference](docs/config.md)
- [Docker quickstart](docs/docker-quickstart.md)
- [Troubleshooting](docs/troubleshooting.md)
- [PRD status](docs/prd-status.md)
- [Patreon notes](docs/patreon.md)
- [Product PRD](serial-sync-prd.md)

## License

Apache-2.0.
