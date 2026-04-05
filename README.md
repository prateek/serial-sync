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

## What It Does

- syncs releases from provider-backed sources
- classifies them into story tracks
- materializes canonical artifacts on disk
- publishes those artifacts to filesystem or exec-hook targets
- records runs, events, and publish history in SQLite

## Current Scope

- Patreon is the first provider
- live Patreon `username_password` bootstrap, TOTP-assisted login, session import, and persisted session reuse are implemented
- creator-feed and collection Patreon sources are implemented
- `wizard`, `run once`, `auth bootstrap`, `auth import-session`, `publish-record inspect`, and a single-process `daemon` are implemented
- the Docker image includes Chromium and Xvfb for first-run Patreon bootstrap inside the container
- the daemon exposes `/healthz`, `/status`, and `/metrics`
- the bundled fixture demo still exists in `examples/config.demo.toml`
- `filesystem` and `exec` publishing are implemented
- static binary release packaging is configured through `.goreleaser.yml`

## More

- [Developer guide](DEVELOPER.md)
- [First source walkthrough](docs/first-source.md)
- [Config reference](docs/config.md)
- [Docker quickstart](docs/docker-quickstart.md)
- [Troubleshooting](docs/troubleshooting.md)
- [PRD status](docs/prd-status.md)
- [Patreon notes](docs/patreon.md)
- [Product PRD](serial-sync-prd.md)

## License

Apache-2.0.
