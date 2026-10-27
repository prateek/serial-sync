# First source walkthrough

First complete the [credential-free Docker fixture](https://github.com/prateek/serial-sync/blob/main/docs/docker-quickstart.md#first-reading-copy-without-an-account).
These steps then use **one** Patreon creator you already support. Run commands
from the repository root with the locally built default/full `serial-sync` image.
Use full for first authentication and Python-assisted recovery/testing. A steady
container can reuse a saved session, but browser reauthentication needs full with
the same config/state/output mounts; stop the other runtime before switching.

## 1. Initialize a private config

```sh
(
  umask 077
  docker run --rm --network none --user "$(id -u):$(id -g)" \
    -v "$PWD:/config" serial-sync setup init --path /config/config.toml
  chmod 600 config.toml
  mkdir -p serial-sync-private
  chmod 700 serial-sync-private
)
```

`setup init` creates one auth profile and one filesystem publisher, with no
active sources or series. Runtime paths use `/state` automatically in Docker.
Its commented mapping shows grouped inputs and inherited defaults. Keep the
publisher at `/state/published` for this walkthrough; host-visible output can be
read through a temporary read-only volume mount after the run.

Create the credentials file privately, then edit it in your local editor:

```sh
(umask 077; touch patreon.env)
chmod 600 patreon.env
```

Add `PATREON_USERNAME=...` and `PATREON_PASSWORD=...`; add
`PATREON_TOTP_SECRET=...` only if using an authenticator seed. Do not put secrets
in shell commands, config, issue reports or screenshots. Git and Docker ignore
the documented config, env and session filenames; files elsewhere need equivalent
exclusions. Treat state and dump workspaces as private too: they contain cookies,
creator content and private URLs. The private parent directory protects dump
content even when files inside it are created with broader modes. Do not pre-create its `workspace` child: dump
creates and marks that directory, and refuses existing unrecognized directories.
The ownership command after dumping makes host editing possible on Linux.

Define a helper for the commands that may contact Patreon and write state:

```sh
ss_live() {
  docker run --rm \
    --env-file "$PWD/patreon.env" \
    -v serial-sync-state:/state \
    -v "$PWD/config.toml:/config/config.toml:ro" \
    -v "$PWD/serial-sync-private:/private" \
    serial-sync "$@"
}
```

## 2. Authenticate and select one creator

```sh
ss_live setup auth --auth-profile patreon-default
ss_live setup memberships --auth-profile patreon-default
```

Memberships lists metadata without fetching posts or opening a browser. Pick a
full creator handle or name from the list. Replace `YOUR_CREATOR_HANDLE` in the
next command with a value unique across that list. `--creator` matches substrings
of IDs, handles and names; a short or overlapping value can select several
creators. It is a filter, not an exact identity selector.
If a challenge needs interaction, use the loopback-only
[noVNC and SSH tunnel flow](https://github.com/prateek/serial-sync/blob/main/docs/docker-quickstart.md#compose-and-private-remote-authentication),
sharing the same named volume as described there.

If you already have a compatible session bundle, protect it with `chmod 600`
and import it instead of logging in (do not attach it to an issue):

```sh
docker run --rm \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD/patreon-session.json:/tmp/patreon-session.json:ro" \
  serial-sync setup auth --import-session /tmp/patreon-session.json --auth-profile patreon-default
```

## 3. Dump once, then edit offline

```sh
ss_live setup dump --auth-profile patreon-default \
  --creator YOUR_CREATOR_HANDLE --path /private/workspace
```

Confirm the dump summary reports `creators=1`. If it reports more, stop before
merging sources and choose a unique full handle or name before refreshing. If
no value uniquely selects your creator, this filter cannot guarantee a one-creator
dump; do not continue this first-creator path with an ambiguous selection.

The command prints `sources=/private/workspace/captures/<generation>/sources.toml`.
On the host that is `serial-sync-private/workspace/captures/<generation>/sources.toml`.
The workspace contains `manifest.json`, `series.toml`, normalized
`captures/<generation>/creators/<source-id>/posts.ndjson`, raw `posts/*.json`
and downloaded `attachments/`. Resolve the active generation through the
manifest; do not combine generations. A refresh repeats the same dump command,
installs a complete generation atomically and preserves your authored files.
A failed refresh leaves the previous capture usable.

After each successful dump, give only this private workspace back to your host
user before editing (the container writes as root on a normal Linux host):

```sh
docker run --rm --network none --entrypoint chown \
  -v "$PWD/serial-sync-private:/private" \
  serial-sync -R "$(id -u):$(id -g)" /private/workspace
```

The parent remains mode 700. Inspect `posts.ndjson` and edit
**`serial-sync-private/workspace/series.toml`** in
your editor. Replace the draft with the story mappings you want.
The generated grouped scaffold is commented and inherits output/content defaults;
uncomment/customize it or replace it deliberately. Refresh preserves existing
`series.toml` and README files, so older workspaces may retain their prior draft.

For example, a text-post story can use this shape, with the real source ID and
title pattern:

```toml
[[series]]
id = "my-story"
title = "My Story"
source = "source-id-from-dump"
authors = ["Creator Name"]

[series.output]
format = "epub"

[[series.inputs]]
title_patterns = ["^My Story - Chapter "]
```

Sources describe **what to fetch**; series and their inputs decide **which story
gets each release**. Output belongs to the series. Source defaults can supply
shared input policy. Grouped inputs inherit `text_post` and `chapter`, with a
1,500 visible-character guard; set `min_body_chars = 0` only for deliberate short
fiction or attachment-only mappings. For attachment selection, collection IDs,
review holds and exclusions, use [the rules guide](https://github.com/prateek/serial-sync/blob/main/docs/rules.md) and
[config defaults](https://github.com/prateek/serial-sync/blob/main/docs/config.md#authoring-defaults-and-exceptions).
Book labels establish identity; they do not establish complete chapter coverage.

## 4. Preview offline, merge and check

```sh
docker run --rm --network none --read-only \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  -v "$PWD/serial-sync-private/workspace:/workspace:ro" \
  serial-sync setup preview --workspace /workspace \
    --series-file /workspace/series.toml --show-posts
```

Iterate on the series file and repeat preview until assignments and unmatched or
held posts are understood. No writable state mount, credentials or provider
connection is needed. Preview checks classification; it does not create EPUBs.

In your editor, append the complete `[[sources]]` entries from the **reported
capture's `sources.toml`** and the authored `[[series]]` entries from
`series.toml` to the host's **`config.toml`**. Replace entries with the same IDs
on later edits rather than duplicating them. A dump or preview never installs
these mappings in the runtime config for you.

```sh
docker run --rm --network none --read-only \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync setup check
```

Confirm the reported source/series counts match what you merged. Stop before a
live run if the container still reflects old input. The tested macOS OrbStack
engine showed stale reads for some edited bind paths; see the
[fresh snapshot and digest guidance](https://github.com/prateek/serial-sync/blob/main/docs/support.md#packages-and-validation-boundaries)
when validating host edits.

Check rejects unknown keys and invalid rule values without creating a catalog,
logs or run record. Ordinary `run --dry-run` is a **live sync** that can fetch
posts, update caches and write diagnostics; it skips publishing. Use
`setup preview` for normal offline authoring. `run --rebuild --dry-run` is the
separate read-only, offline applied-library planner.

## 5. Run one cycle and inspect the reading copy

Replace `SOURCE_ID` with the merged source ID. After config edits, verify actual
host and container bytes before a live run, even if source/series counts match.
This portable check streams host bytes through the image for hashing and compares
them with the **same file bind** the helper uses; it prints no config contents:

```sh
(
  set -eu
  ss_host_digest=$(docker run --rm -i --network none --read-only \
    --entrypoint sha256sum serial-sync < config.toml)
  ss_mounted_digest=$(docker run --rm --network none --read-only \
    --entrypoint sha256sum \
    -v "$PWD/config.toml:/config/config.toml:ro" serial-sync /config/config.toml)
  if [ "${ss_host_digest%% *}" != "${ss_mounted_digest%% *}" ]; then
    printf 'Config bind differs from host input; stop and use a fresh private snapshot.\n' >&2
    exit 1
  fi
  printf 'Verified config digest: %s\n' "${ss_host_digest%% *}"
)
```

If verification fails, follow the snapshot guidance above and repeat preview/check
with the verified snapshot as the config bind source in all commands, including
`ss_live`. Do not continue with the stale bind or edit config between verification
and the run.

```sh
ss_live run --source SOURCE_ID --target local-files
ss_live debug runs

docker run --rm --network none --entrypoint /bin/sh \
  -v serial-sync-state:/state:ro serial-sync \
  -c 'find /state/published -type f'
```

To copy reading copies to a host directory for your reader:

```sh
mkdir -p published-export
chmod 700 published-export
docker run --rm --network none --entrypoint /bin/sh \
  -v serial-sync-state:/state:ro -v "$PWD/published-export:/export" \
  serial-sync -c 'cp -R /state/published/. /export/'
```

Logs live in `/state/logs`. Use `debug run RUN_ID` or `debug events RUN_ID` to
inspect a run; follow the [safe-sharing guidance](https://github.com/prateek/serial-sync/blob/main/docs/observability.md#safe-sharing)
before creating a support bundle. Repeat an unchanged run to verify that it
skips unchanged releases. Keep the live named volume and private workspace;
the fixture cleanup command is only for `onboarding-demo`.

Scheduling, hooks, volumes, runner and reader integrations are optional later
steps. Read the [support and upgrade contract](https://github.com/prateek/serial-sync/blob/main/docs/support.md) before leaving a
scheduler unattended.

## Rebuild stored output

Use `run --rebuild` after changing output settings or when a completed volume
reports a pending correction. It reads the catalog's captured normalized posts,
raw payloads and attachments. It makes no provider requests and preserves sync
cursors. Missing inputs are reported by release and path; the related old output
stays in place.

Stop active runs, replace `SERIES_ID` with the configured series ID, then preview
that series with state mounted read-only:

```sh
docker run --rm --network none \
  -v serial-sync-state:/state:ro \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run --rebuild --dry-run --series SERIES_ID --target local-files
```

Check the additions, replacements and retirements. An open range is informational;
a blocked replacement or ownership conflict needs attention. Use `--source` only
when the selected replacement can be built entirely from that source. A volume
with members from an excluded source stays intact and the command returns nonzero.
Both preview and rebuild exclude disabled sources. Enable every source needed by
a shared volume before rebuilding it.

The JSON includes the selected `scope`. If `pending_deliveries` is present, those
saved editions must finish before the current output plan. Changed target settings
or a narrower scope are reported as blocked. Restore the pending target's settings
and retry its original scope first. Preview uses an in-memory catalog snapshot;
an active, uncheckpointed catalog is rejected without changing state.

Rebuild the chosen scope with writable state:

```sh
docker run --rm --network none \
  -v serial-sync-state:/state \
  -v "$PWD/config.toml:/config/config.toml:ro" \
  serial-sync run --rebuild --series SERIES_ID --target local-files
```

Mount any publisher directory outside `/state` in both commands, read-only during
preview and writable during rebuild. Repeating an unchanged rebuild reuses stored
validated output. Pending deliveries resume from their saved plan before later
changes are delivered; a failed target leaves successful targets intact.

If regrouping reports a cyclic same-path replacement, give the series a distinct
output title for that rebuild. This lets all replacement files arrive before old
paths are retired. A later rebuild can restore the preferred title.
The dependency graph is checked before saving new delivery work. An undelivered
cyclic plan saved by an older version is replanned automatically on retry.

Before migrating a large library, import a small rebuilt series into Calibre and
read it on the intended device. Check chapter titles, author notes and series
order. Replacing a delivered volume may affect the reader's saved position.
Previous artifacts remain archived in state; only covered, owned published files
are retired after their replacements succeed. Legacy libraries keep their old
output on ordinary runs until this explicit migration.

## Revising a configured source

Copy your current config to `config.proposed.toml` privately and edit that copy.
Stop active runs, then compare it with the baseline using the **same** named
state volume used throughout this walkthrough. Both configs are mounted explicitly:

```sh
docker run --rm --network none --read-only \
  -v serial-sync-state:/state:ro \
  -v "$PWD/config.toml:/config/baseline.toml:ro" \
  -v "$PWD/config.proposed.toml:/config/config.toml:ro" \
  serial-sync setup preview --stored --compare /config/baseline.toml --show-posts
```

If a publisher is outside `/state`, also mount its destination read-only so the
applied-library planner can check ownership. Existing Compose users must select
their existing state volume as explained in the
[Compose upgrade note](https://github.com/prateek/serial-sync/blob/main/docs/support.md#upgrading-from-the-earlier-compose-example),
rather than opening a new catalog. Workspace replay alone cannot report applied
library changes without a catalog.

For a new creator, add `--suggest` to workspace preview to print a starting config
fragment. Choose series names, inspect candidate members, and copy reviewed
entries into the proposed config. Series own output choices; source defaults
supply shared input policy, `sources.author` supplies author identity, and
`series.source` supplies the common source. Use guarded collection or tag lists
for fiction and `[[review]]` with a reason for administrative posts. Preview again
and inspect failed guards with `--show-posts`.

Review classification, output-policy and applied-library plans separately. After
promoting the config, preview `run --rebuild --dry-run` before applying stored
output changes with `run --rebuild`. Book collection/tag labels establish identity;
declare the chapter endpoint separately before expecting a completed volume.
See [discovery candidates](https://github.com/prateek/serial-sync/blob/main/docs/rules.md#discovery-candidates)
for evidence-scoped dismissals.
