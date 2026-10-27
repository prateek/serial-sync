# serial-sync

`serial-sync` syncs serialized content from authenticated sources into durable artifacts and replayable publishers.

## Quickstart

Start with the [credential-free Docker walkthrough](docs/docker-quickstart.md).
It builds the image, mounts a synthetic fixture and a disposable host directory,
and produces one EPUB you can open without an account. It includes repeat-run,
read-only preview and cleanup commands.

Then follow the [first-source walkthrough](docs/first-source.md):

1. Initialize a minimal auth/publisher config with `setup init`.
2. Authenticate and select one creator.
3. Capture that creator with `setup dump --creator HANDLE`.
4. Edit grouped series inputs and use offline `setup preview`.
5. Merge the dump's sources and your series into the runtime config; `setup check`.
6. Run one containerized sync and inspect the reading copy.

Sources define upstream access; series and inputs map releases to stories, with
output at the series layer. A dump or preview does not install those mappings.
`setup check` and `setup preview` are offline and read-only. Ordinary
`run --dry-run` can fetch data and write run diagnostics; it skips publication.
Keep credentials and captures private, and use the documented loopback-only
noVNC/SSH tunnel path when bootstrap needs an interactive challenge.

Patreon attachment reuse verifies captured bytes and follows the post revision and
media identity. See [attachment freshness](docs/config.md#patreon-attachment-freshness)
for correction handling and signed URL reuse. Dump refresh installs a complete
capture generation atomically and preserves authored files and the prior capture
on failure. Strict checks and previews need no writable state or provider session.

The optional [steady container runtime](docs/browser-runtime.md) reuses saved
sessions without installing a browser. The default full image retains automatic
bootstrap and noVNC reauthentication.

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
- the default/full Docker image includes Google Chrome on `amd64` or Chromium on `arm64`, plus Xvfb, poppler's `pdftohtml`, EPUBCheck, and an optional noVNC auth wrapper for first-run Patreon bootstrap inside the container
- the optional steady image reuses saved sessions without a browser; full is required for browser reauthentication and Python-assisted recovery/testing, with the same state/config mounts
- the daemon exposes `/healthz`, `/status`, and `/metrics`
- runs write human-readable and JSONL logs under `runtime.log_root`; [opt-in diagnostic retention and recovery](docs/observability.md#diagnostic-retention-and-recovery) bounds history with `debug cleanup`
- the bundled fixture demo still exists in `examples/config.demo.toml`
- `filesystem`, `drop`, and `exec` publishing are implemented; a non-dry-run `run` pings a Healthchecks.io check when `SERIAL_SYNC_HEALTHCHECK_URL` is set
- series output can preserve source attachments or emit EPUBCheck-validated EPUB with portable metadata and artwork, including EPUB 2 attachments; standalone chapters end with their original content, and assembled books/volumes can carry one author page
- published artifact filenames are lowercase, dash-slugged, and stable enough for shells, URLs, and sync tools
- generated chapter EPUBs are titled by the chapter ("Chapter 430", "Book Two, Chapter 71"), never the series name, carry a body-matter landmark and an "Author's note" TOC entry for the post preface, and have a Calibre/EPUB 3 series index that follows the author's numbering (`book.chapter` when it restarts per book); interludes share the preceding chapter's index
- curated author profiles and series/book artwork are retained outside content identity; `metadata.identity_source = "release"` repairs unreliable attachment titles/authors from the post and configured author; existing copies adopt metadata edits only through explicit rebuild
- the [BookOrbit guide](integrations/bookorbit/README.md) drops each new chapter once into a watched library folder; BookOrbit imports, renames, notifies, and handles unfollowed series. The optional [Serial Reader experiment](integrations/bookorbit/reader/README.md) adds Previous/Next series navigation
- optional volumes follow declared author books, falling back to configurable 50-chapter ranges; gaps keep chapters as singles
- `run --rebuild` applies output changes offline from captured inputs; completed volumes remain unchanged during ordinary sync
- immutable reading-copy captures bind metadata, provenance and retained inputs; identical reading bytes can share a blob. The catalog migration is not writable by older binaries; keep a consistent whole-state backup for rollback
- diagnostic expiry is disabled by default and unscheduled; explicit cleanup may expire finished diagnostics and completed recovery-plan receipts while protecting pending plans, catalog records and captured content
- the container is the primary package; see [support, platforms and upgrades](docs/support.md) for version pinning and validation limits. The v0.1.0 release assets package the optional runner, while the same tag also produced a dual-architecture core image; neither naming nor packaging establishes a stable core compatibility promise

## More

- [Contributing and first tasks](CONTRIBUTING.md)
- [Provider contribution guide](docs/provider-contributing.md)
- [Supported platforms and upgrades](docs/support.md)
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
4. Edit series and `setup preview` offline
5. Merge sources/series into config and `setup check`
6. `run`
7. `debug run <run-id>` if something looks wrong

## Rebuild an existing library

Normal runs retain legacy filenames and completed volumes. Preview an explicit
migration from the stored catalog before rebuilding a series. Rebuild also checks
chapter reading-copy editions, so presentation changes can update a volume even
when upstream text is unchanged. Unknown member bytes are reported as a
conditional rebuild: the volume may change after regeneration, while identical
editions keep the current output. A definite replacement or retirement produces an unconditional reading-position
notice; item-level conditional flags remain intact.
The updated recipe refreshes older volumes on
explicit rebuild; preview and apply one series at a time with `--series <series-id>`:

```sh
docker run --rm --network none \
  -v serial-sync-state:/state:ro \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run --rebuild --dry-run --series main-story --target local-files
```

Stop active runs before previewing. The preview lists replacement destinations,
retirements, blocked inputs and rejected replacement sets without changing the
catalog or calling hooks: a same-path replacement that would not cover its old
volume, a cyclic rename, or a retirement whose file was edited outside the
library fails the run, and a plan that collides with edited destination bytes is
blocked until the file is restored or the ownership resolved.
Preview checks archived reading-copy hashes as well as metadata: a missing or
corrupt archived copy needs rebuilding even when its published copy is intact.
It checks captured inputs without writing captures or running converters; input
failures block preview and execution with the same reason. Archive repairs may
show a potential replacement and reading-position warning: preview does not
regenerate bytes. A rebuild that reproduces the delivered bytes can skip delivery.
When disabling volume bundling, rebuild preview lists replacement chapter files and retirement of the last active volume, even when no volumes remain.
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
Reading-copy captures retain their metadata and provenance separately from shared
reading bytes. A metadata-only rebuild keeps older captures readable and skips
delivery when the destination already contains identical bytes. See the
[capture compatibility notes](docs/config.md#portable-publication-metadata).

`setup preview --stored --compare <baseline>` also shows library changes through
the rebuild planner, with destination ownership checks and blocked inputs.

`setup memberships` lists membership metadata using a saved session, including
whether each creator has an enabled, mapped source. It fetches no posts and never
opens a browser. A profile-only config is sufficient; use `setup auth` first.
`setup enrich` adds collection identities from stored raw JSON without refetching
posts or changing published files. Preview can enrich in memory without this step.

## License

Core Go code, the optional runner, project-authored docs and synthetic fixtures
use [Apache-2.0](LICENSE), unless a file carries another notice. The root license
contains the complete [canonical Apache text](https://www.apache.org/licenses/LICENSE-2.0.txt).
The optional [Serial Reader experiment](integrations/bookorbit/reader/README.md)
has its own [AGPLv3 license](integrations/bookorbit/reader/LICENSE),
[additional terms](integrations/bookorbit/reader/ADDITIONAL_TERMS.md) and
[notices](integrations/bookorbit/reader/NOTICE); those terms are unchanged.
Bundled dependencies and upstream reader material retain their own licenses.
Fetching creator content grants no redistribution rights to that content.
