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

If Patreon serves a Cloudflare or other interactive challenge during bootstrap, run
`setup auth` once in a visible browser session, the bundled noVNC auth container,
or import a session bundle, then return to the containerized `setup dump` / `run`
flow.

If you want to author series definitions against a local dump instead of repeatedly hitting Patreon:

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
  serial-sync setup preview --workspace /work/serial-sync-rule-workspace --series-file /work/serial-sync-rule-workspace/series.toml --show-posts
```

That dump workspace keeps both the fast authoring view (`posts.ndjson`) and the
full capture (`creators/<source-id>/posts/*.json` plus `attachments/`) so later
offline replay/materialization work can reuse the same dump without re-fetching Patreon.

## What It Does

- syncs releases from provider-backed sources
- classifies them into series
- materializes canonical artifacts on disk
- publishes those artifacts to filesystem or exec-hook targets
- records runs, events, and publish history in SQLite

## Current Scope

- Patreon is the first provider
- live Patreon `username_password` bootstrap, TOTP-assisted login, session import, and persisted session reuse are implemented
- Patreon membership-driven source dumping is implemented through `setup dump`
- dump-first series authoring is implemented through `setup dump` and `setup preview`
- `setup dump` now captures normalized posts, raw Patreon post JSON, and downloaded attachments into the same workspace
- creator-feed and collection Patreon sources are implemented
- `setup auth`, `run`, `debug`, and `run daemon` are implemented
- the Docker image includes Google Chrome on `amd64` or Chromium on `arm64`, plus Xvfb and an optional noVNC auth wrapper for first-run Patreon bootstrap inside the container
- the daemon exposes `/healthz`, `/status`, and `/metrics`
- every run now writes both human-readable and JSONL logs under `runtime.log_root`, and support bundles include those logs
- the bundled fixture demo still exists in `examples/config.demo.toml`
- `filesystem` and `exec` publishing are implemented
- series output can preserve source attachments or emit EPUB, including a prefaced EPUB path for attachment-backed Patreon posts
- static binary release packaging is configured through `.goreleaser.yml`

## More

- [Developer guide](DEVELOPER.md)
- [First source walkthrough](docs/first-source.md)
- [Series authoring guide](docs/rules.md)
- [Config reference](docs/config.md)
- [Docker quickstart](docs/docker-quickstart.md)
- [Observability guide](docs/observability.md)
- [Troubleshooting](docs/troubleshooting.md)
- [PRD status](docs/prd-status.md)
- [Patreon notes](docs/patreon.md)
- [Product PRD](serial-sync-prd.md)

## CLI Shape

The public CLI is now intentionally small:

- `setup`: config, auth, source dumps, and series preview
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
