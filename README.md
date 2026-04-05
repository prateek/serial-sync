# serial-sync

`serial-sync` syncs serialized content from authenticated sources into durable artifacts and replayable publishers.

## Quickstart

```sh
export PATREON_USERNAME="you@example.com"
export PATREON_PASSWORD="your-password"
go run ./cmd/serial-sync init
```

Edit the generated config with your Patreon creator URL and rules, then start with:

```sh
go run ./cmd/serial-sync sync --dry-run
```

## What It Does

- syncs releases from provider-backed sources
- classifies them into story tracks
- materializes canonical artifacts on disk
- publishes those artifacts to filesystem or exec-hook targets
- records runs, events, and publish history in SQLite

## Current Scope

- Patreon is the first provider
- live Patreon `username_password` bootstrap and persisted session reuse are implemented
- the bundled fixture demo still exists in `examples/config.demo.toml`
- `filesystem` and `exec` publishing are implemented
- `wizard`, `daemon`, and session-bundle import are still future work

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
