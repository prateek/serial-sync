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
  serial-sync run
```

If you want to author rules against a local dump instead of repeatedly hitting Patreon:

```sh
docker run --rm \
  --env-file ./patreon.env \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD:/work" \
  serial-sync setup dump --auth-profile patreon-default --path /work/serial-sync-rule-workspace --force

docker run --rm \
  -v "$PWD:/work" \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync setup preview --workspace /work/serial-sync-rule-workspace --rules-file /work/serial-sync-rule-workspace/rules.toml --show-posts
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
- Patreon membership-driven source dumping is implemented through `setup dump`
- dump-first rule authoring is implemented through `setup dump` and `setup preview`
- creator-feed and collection Patreon sources are implemented
- `setup auth`, `run`, `debug`, and `run daemon` are implemented
- the Docker image includes Chromium and Xvfb for first-run Patreon bootstrap inside the container
- the daemon exposes `/healthz`, `/status`, and `/metrics`
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
- [Observability guide](docs/observability.md)
- [Troubleshooting](docs/troubleshooting.md)
- [PRD status](docs/prd-status.md)
- [Patreon notes](docs/patreon.md)
- [Product PRD](serial-sync-prd.md)

## CLI Shape

The public CLI is now intentionally small:

- `setup`: config, auth, source dumps, and rules preview
- `run`: the normal sync-plus-publish execution path, plus `run daemon`
- `debug`: run forensics, publish record inspection, and support bundles

Old command names are documented in the migration notes inside the guides, but the default user path is now:

1. `setup init`
2. `setup auth`
3. `setup dump`
4. `setup preview`
5. `run`
6. `debug run <run-id>` if something looks wrong

## License

Apache-2.0.
