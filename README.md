# serial-sync

`serial-sync` syncs serialized content from authenticated sources into durable artifacts and replayable publishers.

## Quickstart

```sh
docker build -t serial-sync .
printf 'PATREON_USERNAME=%s\nPATREON_PASSWORD=%s\n' "you@example.com" "your-password" > patreon.env
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run once
```

If you want `serial-sync` to suggest sources from your current Patreon account before you hand-edit rules:

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync source discover --auth-profile patreon-default --format toml
```

## What It Does

- syncs releases from provider-backed sources
- classifies them into story tracks
- materializes canonical artifacts on disk
- publishes those artifacts to filesystem or exec-hook targets
- records runs, events, and publish history in SQLite

## Current Scope

- Patreon is the first provider
- live Patreon `username_password` bootstrap, TOTP-assisted login, session import, and persisted session reuse are implemented
- Patreon membership discovery and source-config suggestion are implemented through `source discover` and daemon discovery endpoints
- creator-feed and collection Patreon sources are implemented
- `wizard`, `run once`, `auth bootstrap`, `auth import-session`, `publish-record inspect`, and a single-process `daemon` are implemented
- the Docker image includes Chromium and Xvfb for first-run Patreon bootstrap inside the container
- the daemon exposes `/healthz`, `/status`, `/metrics`, `/discover/sources`, and `/discover/config`
- every run now writes both human-readable and JSONL logs under `runtime.log_root`, and support bundles include those logs
- the bundled fixture demo still exists in `examples/config.demo.toml`
- `filesystem` and `exec` publishing are implemented
- static binary release packaging is configured through `.goreleaser.yml`

## More

- [Developer guide](DEVELOPER.md)
- [First source walkthrough](docs/first-source.md)
- [Rule authoring guide](docs/rules.md)
- [Config reference](docs/config.md)
- [Docker quickstart](docs/docker-quickstart.md)
- [Source discovery guide](docs/source-discovery.md)
- [Observability guide](docs/observability.md)
- [Troubleshooting](docs/troubleshooting.md)
- [PRD status](docs/prd-status.md)
- [Patreon notes](docs/patreon.md)
- [Product PRD](serial-sync-prd.md)

## License

Apache-2.0.
