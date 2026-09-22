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
  serial-sync setup dump --auth-profile patreon-default --path /work/serial-sync-rule-workspace

docker run --rm \
  -v "$PWD:/work" \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync setup preview --workspace /work/serial-sync-rule-workspace --series-file /work/serial-sync-rule-workspace/series.toml --show-posts
```

That dump workspace keeps both the fast authoring view (`posts.ndjson`) and the
full capture (`captures/<generation>/creators/<source-id>/posts/*.json` plus `attachments/`) so later
offline replay/materialization work can reuse the same dump without re-fetching Patreon.

Repeat `setup dump` to refresh a recognized workspace. A failed refresh retains
the previous capture and your authored files. `setup check` and `setup preview`
are offline and read-only; they work without writable state or a database.
Both reject unknown config keys and invalid rule values before provider work.

## What It Does

- syncs releases from provider-backed sources
- classifies them into series
- materializes canonical artifacts on disk
- publishes those artifacts to filesystem or exec-hook targets
- keeps the catalog, runs, and publish receipts in SQLite; detailed events and captured inputs stay on disk

## Current Scope

- Patreon is the first provider
- live Patreon `username_password` bootstrap, TOTP-assisted login, session import, and persisted session reuse are implemented
- Patreon membership-driven source dumping is implemented through `setup dump`
- dump-first series authoring uses `setup dump` and read-only `setup preview`; `--stored --compare <config>` replays catalog releases through two configs offline
- advisory discovery reports possible new series even behind existing mappings; `setup candidates dismiss <id> --reason` dismisses current evidence
- `setup dump` now captures normalized posts, raw Patreon post JSON, and downloaded attachments into the same workspace
- creator-feed and collection Patreon sources are implemented
- `setup auth`, `run`, `debug`, and `run daemon` are implemented
- the Docker image includes Google Chrome on `amd64` or Chromium on `arm64`, plus Xvfb, Calibre, EPUBCheck, and an optional noVNC auth wrapper for first-run Patreon bootstrap inside the container
- the daemon exposes `/healthz`, `/status`, and `/metrics`
- every run now writes both human-readable and JSONL logs under `runtime.log_root`, and support bundles include those logs
- the bundled fixture demo still exists in `examples/config.demo.toml`
- `filesystem` and `exec` publishing are implemented
- series output can preserve source attachments or emit EPUBCheck-validated EPUB with portable metadata, artwork, and a final About page, including EPUB 2 attachments
- published artifact filenames are lowercase, dash-slugged, and stable enough for shells, URLs, and sync tools
- generated chapter EPUBs have distinct titles and Calibre/EPUB 3 series positions
- curated author profiles and series/book artwork are retained outside content identity; `metadata.identity_source = "release"` repairs unreliable attachment titles/authors from the post and configured author; existing copies adopt metadata edits only through explicit rebuild
- the [BookOrbit adapter](integrations/bookorbit/README.md) verifies readable imports and groups new-release notifications through ntfy with links to the reader; deploy stock BookOrbit by default. The optional [Serial Reader experiment](integrations/bookorbit/reader/README.md) adds Previous/Next series navigation
- optional volumes follow declared author books, falling back to configurable 50-chapter ranges; gaps keep chapters as singles
- `run --rebuild` applies output changes offline from captured inputs; completed volumes remain unchanged during ordinary sync
- static binary release packaging is configured through `.goreleaser.yml`

## More

- [Developer guide](DEVELOPER.md)
- [First source walkthrough](docs/first-source.md)
- [Series authoring guide](docs/rules.md)
- [Config reference](docs/config.md)
- [Hook contracts](docs/hooks.md)
- [Docker quickstart](docs/docker-quickstart.md)
- [Observability guide](docs/observability.md)
- [Troubleshooting](docs/troubleshooting.md)
- [PRD status](docs/prd-status.md)
- [Patreon notes](docs/patreon.md)
- [Product PRD](serial-sync-prd.md)

## CLI Shape

The public CLI is now intentionally small:

- `setup`: config, auth, source dumps, offline preview, and discovery candidates
- `run`: the normal sync-plus-publish execution path, plus `run daemon`
- `debug`: run forensics, publish record inspection, and support bundles

The default user path is:

1. `setup init`
2. `setup auth`
3. `setup dump`
4. `setup preview`
5. `run`
6. `debug run <run-id>` if something looks wrong

## Rebuild an existing library

Normal runs retain legacy filenames and completed volumes. Preview an explicit
migration from the stored catalog before rebuilding a series:

```sh
docker run --rm --network none \
  -v serial-sync-state:/state:ro \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run --rebuild --dry-run --series main-story --target local-files
```

Stop active runs before previewing. The preview lists replacement destinations,
retirements and blocked inputs without changing the catalog or calling hooks.
Preview and rebuild both exclude disabled sources and retain their published files.
If the publisher lives outside `/state`, mount its folder too, read-only for the
preview. See [the rebuild walkthrough](docs/first-source.md#rebuild-stored-output)
for the writable command and a small reader sample before a larger migration.

Grouped label and pattern inputs, visible-body guards, inherited defaults, and
reasoned review/override rules keep mappings compact. Pin collection IDs to keep
matching through renames; preview reports possible label drift and conflicts.
`hold_candidates = true` on a broad input holds first chapters, numbering resets,
and unknown collections for review. See the
[authoring reference](docs/config.md#authoring-defaults-and-exceptions).
`setup preview --suggest` prints a draft from captured discovery evidence.
Book `collection` and `tag` selectors assign both series and book; expected chapter
ranges still control volume completion. Decimal, suffixed, negative, and part
numbering stays intact and remains single until explicitly assigned a chapter slot.
`setup preview --stored --compare <baseline>` also shows library changes through
the rebuild planner, with destination ownership checks and blocked inputs.

`setup memberships` lists membership metadata using a saved session, including
whether each creator has an enabled, mapped source. It fetches no posts and never
opens a browser. A profile-only config is sufficient; use `setup auth` first.
`setup enrich` adds collection identities from stored raw JSON without refetching
posts or changing published files. Preview can enrich in memory without this step.

## License

Apache-2.0.
