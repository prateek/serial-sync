# Support, platforms and upgrades

Serial-sync is a Patreon-first technical preview for operators comfortable with
Docker, private credentials and backups. Maintenance and issue responses are
best effort; upstream provider breakage may take time to fix. Start with one
creator and verify a reading copy before scheduling. There is no stable config
or database downgrade guarantee and no supported native Windows package.

## Packages and validation boundaries

The supported user path is a Linux container with config at `/config/config.toml`
and mutable state at `/state`. Docker Desktop supplies that Linux runtime on
macOS. The container publication workflow builds `linux/amd64` and `linux/arm64`;
a successful build does not qualify live Patreon bootstrap on both architectures.
The onboarding fixture is validated on macOS with OrbStack and a Linux arm64
engine. Docker Desktop is a described container path but was not the tested engine. Live login, challenge handling and session import require separate
account-specific qualification; they were not exercised for this walkthrough.
Linux host ownership and SELinux mount labels may need local adjustment. The
browser uses a dedicated unprivileged user; the CLI may create root-owned bind
mount files. Do not solve permission errors by exposing private files globally.

On the tested macOS OrbStack runtime, a host config edit could leave a subsequent
container reading the old bytes from an already used bind path. Replacing the
file or mounting its directory did not consistently refresh it in the synthetic
probe. The qualified test stages the reviewed config at a **fresh private filename**,
leaves that snapshot unchanged while mounted, and verifies its bytes against the
host before syncing. If your config check or preview still reflects old input,
stop before a live run. Compare the host and container file digests, then use a
fresh private snapshot as the bind source at `/config/config.toml`. Do not edit a
mounted snapshot in place. This observation is specific to the tested runtime;
Docker Desktop was not used for these checks.

For example, on macOS compare the host digest with the container's digest without
printing config contents (Linux hosts can use `sha256sum` for the first line):

```sh
shasum -a 256 config.toml
docker run --rm --network none --read-only --entrypoint sha256sum \
  -v "$PWD/config.toml:/config/config.toml:ro" serial-sync /config/config.toml
```

If they differ, stop. Copy the reviewed config to a new mode-600 file inside the
mode-700 private parent and use that fresh file as the bind source. Keep its
container destination `/config/config.toml` and verify the digests again before
running. Use the snapshot as the config bind source in every preview/check/run
command, including the `ss_live` helper's config mount; keep its container target
`/config/config.toml`. Repeat preview/check against that same snapshot before
running. Use a new filename after each edit rather than replacing a mounted
snapshot.

Build the checkout with `docker build -t serial-sync .`. CI's container workflow
publishes `ghcr.io/prateek/serial-sync:sha-<7-character-commit>` and a moving
`:main` tag. Record the full commit and pin the resolved image digest for an
installation; `:main` is an update channel, not a reproducible version. Inspect
`org.opencontainers.image.revision` on a published image to identify its source.
A local build's provenance is the checkout you built; it need not carry that label.
The same workflow also publishes `X.Y.Z`, `X.Y` and `X` core image tags on `v*`
tag pushes. For `v0.1.0`, the tag peels to
`f20a4488d5bc586188a3183c386d42fd3ccccb1e`. The
[BookOrbit runner integration release](https://github.com/prateek/serial-sync/releases/tag/v0.1.0)
contains only `serial-sync-runner-v0.1.0.tar.gz` and `SHA256SUMS` as release assets.
Separately, `ghcr.io/prateek/serial-sync:0.1.0` is a core image index with
`linux/amd64` and `linux/arm64` manifests, verified at digest
`sha256:38d429c0206b8aa0cc0aa33431129bcb6617c6ffea5f4fbea319fd391f6e5a73`.
These packaging facts do not establish a stable core compatibility promise;
pin the intended image digest and inspect its source revision before upgrading.

`.goreleaser.yml` describes Linux/macOS amd64/arm64 archives, but no checked-in
workflow invokes it. Do not assume those core binaries are release assets.
The **v0.1.0 BookOrbit runner integration** archive packages the optional Python
runner. That archive's contents are distinct from the core container images
published by the same Git tag. Its Python 3.11+ and Linux/systemd
requirements, authentication and recovery obligations are documented in the
[runner guide](../integrations/bookorbit/runner/README.md).

## Full and steady image roles

The default/full image includes browser bootstrap and noVNC. The optional
browser-free `steady` runtime reuses a usable saved session and can perform
normal sync and offline preview/rebuild. It cannot open a browser: a missing or
expired session requires reauthentication with the full image. Switch runtimes
only with the same config, state and publisher mounts, stopping other writers
first. See the [browser runtime contract](https://github.com/prateek/serial-sync/blob/main/docs/browser-runtime.md)
for full/steady build and session handoff commands and the loopback-only auth UI.

Python-assisted abandoned-run recovery and `scripts/test-onboarding` require the
**full image**. The steady runtime is not the environment for these Python
procedures. The operator's optional BookOrbit Python runner is a separate host
service with its own requirements; it does not add Python to a steady container.

## Upgrading from the earlier Compose example

The old example read `examples/config.toml` and `examples/patreon.env`, ran a
daemon with a restart policy, and used a project-scoped state volume. Before
adopting the new example, stop the old deployment using its original Compose file
and back up the state. If you have already replaced the file and it cannot parse
without the new paths, identify and stop the existing container directly.

Inspect the old container **before removing it** to record its actual state volume:

```sh
docker inspect CONTAINER_NAME --format '{{range .Mounts}}{{if eq .Destination "/state"}}{{.Name}}{{end}}{{end}}'
```

The default was commonly `examples_serial-sync-state`; a custom Compose project
name changes it. Keep the recorded volume. In the updated example use:

```yaml
volumes:
  serial-sync-state:
    external: true
    name: examples_serial-sync-state # replace with the actual recorded name
```

If using standalone commands instead, replace **every** `-v serial-sync-state:/state`
mount (including read-only variants and the `ss_live` helper) with the recorded
old name. Do not run the new helper unchanged against an existing library. Creating
an empty `serial-sync-state` volume and switching to it loses catalog continuity
and can re-fetch/re-deliver the entire library. A deliberate volume migration
requires a verified, quiesced copy of the full state; it is separate from this
example upgrade.

Move your config/env layout to the repository root only when both root files are
absent. If either exists, stop and compare it with the old deployment config/env
before selecting the existing library; the command refuses to choose for you:

```sh
(
  set -eu
  for file in config.toml patreon.env; do
    if [ -e "$file" ] || [ -L "$file" ]; then
      printf '%s exists; compare with examples/%s before continuing.\n' "$file" "$file" >&2
      exit 1
    fi
    if [ ! -f "examples/$file" ]; then
      printf 'examples/%s is missing; restore both old deployment files first.\n' "$file" >&2
      exit 1
    fi
  done
  umask 077
  cp examples/config.toml config.toml
  cp examples/patreon.env patreon.env
  chmod 600 config.toml patreon.env
)
```

To preserve unattended scheduling, restore these fields under the `serial-sync`
service before using `up -d`:

```yaml
command: ["run", "daemon"]
restart: unless-stopped
```

The new default is one-shot `run`, so `up -d` alone no longer creates a continuing
scheduler. Keep the same project identity, source config and recorded state volume
throughout this transition. The loopback-only auth port and `init: true` apply to
the temporary auth service; leave them in place.

## Upgrade and rollback

1. Record the current image digest/revision and copy the config and private env
   file securely. Stop the daemon, timers and any active auth, dump or sync
   process that shares state; keep a single execution owner.
2. Back up the **whole** `/state` volume while quiesced, including SQLite,
   captures, reading-byte blobs, metadata/normalized/raw sidecars, selected artwork,
   sessions and pending delivery work. Back up the dump workspace, authored config,
   referenced curation assets and any external publisher directory separately.
   Published EPUBs alone cannot restore the catalog, captured inputs or receipts.
   Keep the backup private and verify a restore into a disposable volume.
3. Build or pull a pinned new image. Run `setup check` on your config, the
   credential-free fixture, and offline preview against a restored copy before
   using the live volume. Review release/source changes for migration and output
   policy changes. Existing reading copies adopt output changes through explicit
   preview/rebuild; do not rebuild the whole library as an incidental upgrade.
4. Run one scoped live cycle, inspect logs and the reader result, then restart
   your chosen scheduler. Monitor free bytes **and inodes** (`df -h`, `df -i` on
   the filesystem holding state). With Docker Desktop or OrbStack, inspect the Linux VM's
   filesystem through a container mounting the state volume; macOS host `df -i`
   does not measure the VM's storage. Captures and detailed diagnostics can grow; do not delete
   arbitrary state files to recover space, especially pending delivery inputs.

The immutable-capture catalog migration removes the earlier content-based
artifact uniqueness constraint. Older binaries cannot write that migrated catalog.
Captures bind metadata/provenance and retained inputs even when their reading
bytes share one blob; do not infer archive paths from a filename or content hash.
Use catalog references, retain the complete archive, and inspect missing/corrupt
sidecars before an explicit rebuild. Reading bytes alone cannot recover lost
metadata choices. See the [capture contract](config.md#portable-publication-metadata).

Roll back with the previous image **and its matching quiesced whole-state backup**,
rather than pointing it at a migrated catalog or mixing an older catalog with a
newer artifact root. The latter can leave a pending row pointing at an already
compacted recovery file and trigger a "different settings" refusal. If cleanup
expired the completed file, the symptom can instead be a pending-delivery path
with "no such file or directory". Restore a consistent backup; do not patch/delete
rows or plan files to suppress either refusal. External
deliveries may have happened after the backup; reconcile them before rerunning to
avoid duplicates or retiring user files. Review pending delivery and ownership
diagnostics before an explicit rebuild.

Diagnostic expiry is opt-in and unscheduled: `runtime.diagnostic_retention_days`
defaults to `0`, and setting a positive window alone deletes nothing. Stop writers,
preview with `debug cleanup`, then deliberately use `debug cleanup --apply`.
Completed recovery-plan files may already have compacted into typed receipts
following successful catalog completion; cleanup can expire eligible completed
plan/receipt files and old diagnostics. Durable catalog publish records, pending
plans, captured inputs/metadata and reading copies remain protected. There is no
built-in operator lock or automatic archive offload/budget. See the
[retention/recovery procedure](observability.md#diagnostic-retention-and-recovery)
for writer shutdown, whole-state backup, manual scheduling and full-image Python
repair. An older strict loader may reject `diagnostic_retention_days`; remove that
key when restoring the earlier config/image. Expired diagnostics need a backup,
not merely an image rollback.

## Optional integrations

The ordinary [BookOrbit integration](../integrations/bookorbit/README.md) hands
off files; the reader owns notifications and progress. The runner is an optional
separately authenticated host service. The
[Serial Reader experiment](../integrations/bookorbit/reader/README.md) is a pinned
reader patch with separate licensing and rebase/testing obligations; upstream
vanilla BookOrbit is the default recommendation. Core support does not include
maintaining a reader fork or installing a host runner. Custom hooks and scheduling
are operator choices after a successful one-shot run.
